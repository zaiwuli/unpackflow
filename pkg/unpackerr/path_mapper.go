package unpackerr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	maxArchiveNameBytes = 240
	maxArchivePathBytes = 3000
)

type archiveNameMapper struct {
	mu      sync.Mutex
	entries map[string]string
}

func newArchiveNameMapper() *archiveNameMapper {
	return &archiveNameMapper{entries: make(map[string]string)}
}

func (m *archiveNameMapper) Map(name string) string {
	original := strings.ReplaceAll(name, "\\", "/")
	m.mu.Lock()
	defer m.mu.Unlock()
	if mapped, ok := m.entries[original]; ok {
		return mapped
	}
	parts := strings.Split(strings.TrimPrefix(original, "/"), "/")
	for i := range parts {
		parts[i] = shortenArchiveComponent(parts[i], maxArchiveNameBytes, original)
	}
	mapped := strings.Join(parts, string(filepath.Separator))
	for len([]byte(mapped)) > maxArchivePathBytes {
		longest := -1
		for i, part := range parts {
			if longest < 0 || len([]byte(part)) > len([]byte(parts[longest])) {
				longest = i
			}
		}
		if longest < 0 || len([]byte(parts[longest])) <= 32 {
			break
		}
		parts[longest] = shortenArchiveComponent(parts[longest], len([]byte(parts[longest]))-32, original)
		mapped = strings.Join(parts, string(filepath.Separator))
	}
	m.entries[original] = mapped
	return mapped
}

func shortenArchiveComponent(name string, limit int, seed string) string {
	if len([]byte(name)) <= limit {
		return name
	}
	ext := safeArchiveExtension(name)
	hash := sha256.Sum256([]byte(seed + "\x00" + name))
	suffix := "~" + hex.EncodeToString(hash[:])[:8] + ext
	budget := limit - len([]byte(suffix))
	if budget < 1 {
		budget = 1
	}
	prefixRunes := []rune(name)
	for len([]byte(string(prefixRunes))) > budget && len(prefixRunes) > 0 {
		prefixRunes = prefixRunes[:len(prefixRunes)-1]
	}
	prefix := string(prefixRunes)
	return prefix + suffix
}

// safeArchiveExtension keeps ordinary short extensions while avoiding treating
// a title such as "www.example.com@long text" as one enormous extension.
func safeArchiveExtension(name string) string {
	ext := filepath.Ext(name)
	if ext == "" || ext == name || len([]byte(ext)) > 32 || strings.ContainsAny(ext, `/\\`) {
		return ""
	}
	return ext
}

func (m *archiveNameMapper) Manifest() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]string)
	for original, mapped := range m.entries {
		if original != mapped {
			result[original] = mapped
		}
	}
	return result
}

func (m *archiveNameMapper) WriteManifest(root string) error {
	entries := m.Manifest()
	if len(entries) == 0 {
		return nil
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(entries))
	for _, key := range keys {
		ordered[key] = entries[key]
	}
	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(root, "UnpackFlow-名称映射.json"), append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".archiveflow-part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

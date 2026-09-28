package unpackerr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const n115DateCacheFilename = "unpackflow-date-cids.json"

// n115DateFolderCache stores only the current day's date-folder CIDs.
type n115DateFolderCache struct {
	Date    string            `json:"date"`
	Folders map[string]string `json:"folders"`
}

func (u *Unpackerr) n115DateFolderCID(ctx context.Context, parentCID, date string) (string, error) {
	u.n115DateCacheMu.Lock()
	defer u.n115DateCacheMu.Unlock()

	cachePath := u.n115DateCachePath()
	if u.n115DateCache.Date != date {
		cache, err := readN115DateFolderCache(cachePath, date)
		if err != nil {
			u.Errorf("读取 115 日期目录 CID 缓存失败，将重新查询：%v", err)
			cache = n115DateFolderCache{Date: date, Folders: make(map[string]string)}
		}
		u.n115DateCache = cache
		if err := writeN115DateFolderCache(cachePath, cache); err != nil {
			u.Errorf("清理旧的 115 日期目录 CID 缓存失败：%v", err)
		}
	}
	if cid := strings.TrimSpace(u.n115DateCache.Folders[parentCID]); cid != "" {
		return cid, nil
	}

	cid, err := u.n115FindOrCreateFolder(ctx, parentCID, date)
	if err != nil {
		folders, lookupErr := u.n115ChildFolderCIDs(ctx, parentCID)
		if lookupErr != nil {
			return "", fmt.Errorf("查找已存在日期目录失败（创建错误：%v）：%w", err, lookupErr)
		}
		cid = strings.TrimSpace(folders[date])
		if cid == "" {
			return "", err
		}
	}
	u.n115DateCache.Folders[parentCID] = cid
	if err := writeN115DateFolderCache(cachePath, u.n115DateCache); err != nil {
		u.Errorf("保存 115 日期目录 CID 缓存失败：%v", err)
	}
	return cid, nil
}

func (u *Unpackerr) n115DateCachePath() string {
	base := filepath.Dir(u.ConfigFile)
	if base == "." || base == "" {
		base, _ = os.Getwd()
	}
	return filepath.Join(base, n115DateCacheFilename)
}

func readN115DateFolderCache(path, date string) (n115DateFolderCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return n115DateFolderCache{Date: date, Folders: make(map[string]string)}, nil
		}
		return n115DateFolderCache{}, err
	}
	var cache n115DateFolderCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return n115DateFolderCache{}, err
	}
	if cache.Date != date || cache.Folders == nil {
		cache = n115DateFolderCache{Date: date, Folders: make(map[string]string)}
	}
	return cache, nil
}

func writeN115DateFolderCache(path string, cache n115DateFolderCache) error {
	if cache.Folders == nil {
		cache.Folders = make(map[string]string)
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".unpackflow-date-cids-*.tmp")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	defer os.Remove(tmp)
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

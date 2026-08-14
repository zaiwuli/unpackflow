package clouddrive

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type MonitorConfig struct {
	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

type Monitor struct {
	Client        *Client
	Config        MonitorConfig
	PathOverrides []string
	OnChange      func(Change, []string) error
	OnPush        func(PushMessageInfo)
	OnStatus      func(Status)
	mu            sync.RWMutex
	status        Status
}

type Status struct {
	Running          bool
	Connected        bool
	ChangesReceived  uint64
	MessagesReceived uint64
	IgnoredMessages  uint64
	LastMessageType  int
	LastMessage      time.Time
	PathsMapped      uint64
	LastError        string
	LastChange       time.Time
}

func (m *Monitor) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Monitor) setStatus(update func(*Status)) {
	m.mu.Lock()
	update(&m.status)
	current := m.status
	m.mu.Unlock()
	if m.OnStatus != nil {
		m.OnStatus(current)
	}
}

func (m *Monitor) Run(ctx context.Context) {
	min := m.Config.ReconnectMin
	if min <= 0 {
		min = 5 * time.Second
	}
	max := m.Config.ReconnectMax
	if max < min {
		max = 2 * time.Minute
	}
	backoff := min
	m.setStatus(func(s *Status) { s.Running = true })
	defer m.setStatus(func(s *Status) { s.Running = false; s.Connected = false })
	for ctx.Err() == nil {
		m.setStatus(func(s *Status) { s.Connected = true; s.LastError = "" })
		err := m.Client.SubscribeWithInfo(ctx, func(info PushMessageInfo) {
			m.setStatus(func(s *Status) {
				s.MessagesReceived++
				s.LastMessageType = info.Type
				s.LastMessage = time.Now()
				if !info.FileChange {
					s.IgnoredMessages++
				}
			})
			if m.OnPush != nil {
				m.OnPush(info)
			}
		}, func(change Change) error {
			// Direct path overrides are local and do not depend on CD2's mount
			// point API. Resolve them first so a temporary GetMountPoints failure
			// never drops a real-time file event.
			paths := MapCloudPathWithOverrides(change.Path, nil, m.PathOverrides)
			mounts, mountErr := m.Client.GetMountPoints(ctx)
			if mountErr == nil {
				paths = appendUniquePaths(paths, MapCloudPathWithOverrides(change.Path, mounts, m.PathOverrides))
			}
			if change.NewPath != "" {
				paths = appendUniquePaths(paths, MapCloudPathWithOverrides(change.NewPath, nil, m.PathOverrides))
				if mountErr == nil {
					paths = appendUniquePaths(paths, MapCloudPathWithOverrides(change.NewPath, mounts, m.PathOverrides))
				}
			}
			if mountErr != nil && len(paths) == 0 {
				return mountErr
			}
			m.setStatus(func(s *Status) { s.ChangesReceived++; s.PathsMapped += uint64(len(paths)); s.LastChange = time.Now() })
			if m.OnChange != nil && len(paths) > 0 {
				return m.OnChange(change, paths)
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			backoff = min
		}
		m.setStatus(func(s *Status) {
			s.Connected = false
			if err != nil {
				s.LastError = err.Error()
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < max {
			backoff *= 2
			if backoff > max {
				backoff = max
			}
		}
	}
}

func appendUniquePaths(paths, additions []string) []string {
	for _, value := range additions {
		paths = appendUniquePath(paths, value)
	}
	return paths
}

func MapCloudPath(cloudPath string, mounts []Mount) []string {
	return MapCloudPathWithOverrides(cloudPath, mounts, nil)
}

func MapCloudPathWithOverrides(cloudPath string, mounts []Mount, overrides []string) []string {
	cloudPath = cleanPath(cloudPath)
	var result []string
	// Accept direct cloud-path mappings such as:
	// /115open=>/volume1/CloudNAS/CloudDrive/115open
	// This keeps CD2 usable when its mount-point API reports paths that are not
	// visible inside the container, or when no matching mount point is returned.
	if mapped, ok := applyMatchingOverride(cloudPath, overrides); ok {
		result = appendUniquePath(result, mapped)
	}
	for _, mount := range mounts {
		if !mount.IsMounted {
			continue
		}
		source := cleanPath(mount.SourceDir)
		if source == "." {
			source = "/"
		}
		if cloudPath != source && !strings.HasPrefix(cloudPath, strings.TrimRight(source, "/")+"/") {
			continue
		}
		relative := strings.TrimPrefix(cloudPath, strings.TrimRight(source, "/"))
		server := strings.TrimRight(cleanPath(mount.MountPath), "/") + "/" + strings.TrimLeft(relative, "/")
		result = appendUniquePath(result, applyOverrides(path.Clean(server), overrides))
	}
	return result
}

func appendUniquePath(paths []string, value string) []string {
	for _, existing := range paths {
		if existing == value {
			return paths
		}
	}
	return append(paths, value)
}

func applyMatchingOverride(value string, overrides []string) (string, bool) {
	for _, override := range overrides {
		parts := strings.SplitN(override, "=>", 2)
		if len(parts) != 2 {
			continue
		}
		source := strings.TrimRight(cleanPath(parts[0]), "/")
		target := strings.TrimSpace(parts[1])
		target = strings.ReplaceAll(target, "\\", "/")
		// Keep Windows drive prefixes intact. Calling cleanPath on "G:/..."
		// would turn it into "/G:/...", which filepath cannot open on Windows.
		if len(target) >= 2 && target[1] == ':' {
			target = strings.TrimRight(target, "/")
		} else {
			target = strings.TrimRight(cleanPath(target), "/")
		}
		if value == source || strings.HasPrefix(value, source+"/") {
			mapped := target + strings.TrimPrefix(value, source)
			if len(target) >= 2 && target[1] == ':' {
				return filepath.Clean(filepath.FromSlash(mapped)), true
			}
			return path.Clean(mapped), true
		}
	}
	return "", false
}

func applyOverrides(value string, overrides []string) string {
	for _, override := range overrides {
		parts := strings.SplitN(override, "=>", 2)
		if len(parts) != 2 {
			continue
		}
		host := strings.TrimRight(cleanPath(parts[0]), "/")
		container := strings.TrimRight(cleanPath(parts[1]), "/")
		if value == host || strings.HasPrefix(value, host+"/") {
			return path.Clean(container + strings.TrimPrefix(value, host))
		}
	}
	return value
}

func cleanPath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" {
		return "/"
	}
	cleaned := path.Clean("/" + strings.TrimLeft(value, "/"))
	return cleaned
}

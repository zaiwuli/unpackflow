package unpackerr

import "path/filepath"

// Read existing task metadata only; displaying details must not query 115.
func (u *Unpackerr) fillDashboardTaskDetails(task *DashboardTask, aliases map[string]string, transfers []CD2Transfer) {
	cloudExtract := false
	for _, transfer := range transfers {
		if dashboardCanonicalTaskKey(transfer.Key, aliases) != task.Key {
			continue
		}
		if transfer.Path != "" {
			task.Path = transfer.Path
		}
		if transfer.CachedPath != "" {
			task.CachedPath = transfer.CachedPath
		}
		if transfer.Version.SourceCID != "" {
			task.SourceCID = transfer.Version.SourceCID
		}
		cloudExtract = cloudExtract || transfer.Version.Source == "115"
		if transfer.Version.CloudFile != nil {
			task.FileID = transfer.Version.CloudFile.FID
		}
		if transfer.OutputCID != "" {
			task.OutputCID, task.OutputName = transfer.OutputCID, transfer.OutputName
		}
		if transfer.Retries > task.Retries {
			task.Retries = transfer.Retries
		}
	}
	if u.state != nil {
		u.state.mu.RLock()
		for _, pending := range u.state.Pending {
			if dashboardCanonicalTaskKey(pending.Key, aliases) != task.Key {
				continue
			}
			task.Files = append([]string(nil), pending.Files...)
			if len(pending.Files) > 0 {
				task.Path = archivePrimary(pending.Files)
			}
			if pending.CachedPrimary != "" {
				task.CachedPath = pending.CachedPrimary
			}
			if pending.N115SourceCID != "" {
				task.SourceCID = pending.N115SourceCID
			}
			if pending.N115FID != "" {
				task.FileID = pending.N115FID
			}
			task.NextAttempt = formatDashboardTime(pending.NextAttempt)
			if pending.Attempts > 0 && uint(pending.Attempts) > task.Retries {
				task.Retries = uint(pending.Attempts)
			}
		}
		for _, pending := range u.state.Fallback115 {
			if dashboardCanonicalTaskKey(pending.TaskKey, aliases) != task.Key {
				continue
			}
			task.SourceCID, task.FileID = pending.SourceCID, pending.FID
		}
		u.state.mu.RUnlock()
	}
	if task.SourceCID != "" {
		task.SourceLabel = u.n115CIDRemark(task.SourceCID, "")
		if task.OutputCID != "" || cloudExtract {
			task.TargetCID = u.CloudDrive2.N115ExtractCIDs[task.SourceCID]
			if task.TargetCID == "" {
				task.TargetCID = task.SourceCID
			}
		}
	}
	localPath := task.Path
	if task.CachedPath != "" {
		localPath = task.CachedPath
	}
	if u.folders != nil {
		if folder := u.folders.Folders[localPath]; folder != nil {
			task.OutputPath = folder.config.ExtractPath
			if task.OutputPath == "" {
				task.OutputPath = filepath.Dir(localPath)
			}
			task.Retries = folder.retries
		}
	}
	if task.CachedPath != "" && task.OutputPath == "" {
		task.OutputPath = u.CloudDrive2.CacheExtractPath
	}
}

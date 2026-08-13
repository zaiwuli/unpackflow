package clouddrive

import "testing"

func TestMapCloudPath(t *testing.T) {
	mounts := []Mount{
		{MountPath: "/volume1/CloudDrive", SourceDir: "/", IsMounted: true},
		{MountPath: "/volume1/CloudDrive/115", SourceDir: "/115open", IsMounted: true},
	}
	paths := MapCloudPath("/115open/Movies/movie.rar", mounts)
	if len(paths) != 2 {
		t.Fatalf("expected both matching mounts, got %#v", paths)
	}
	if paths[0] != "/volume1/CloudDrive/115open/Movies/movie.rar" || paths[1] != "/volume1/CloudDrive/115/Movies/movie.rar" {
		t.Fatalf("unexpected mapped paths: %#v", paths)
	}
}

func TestMapCloudPathDoesNotMatchSibling(t *testing.T) {
	paths := MapCloudPath("/115open-other/file.rar", []Mount{{MountPath: "/mnt/cd2", SourceDir: "/115open", IsMounted: true}})
	if len(paths) != 0 {
		t.Fatalf("unexpected sibling match: %#v", paths)
	}
}

func TestMapCloudPathDirectCloudOverride(t *testing.T) {
	paths := MapCloudPathWithOverrides(
		"/115open/上传下载/sample.zip",
		nil,
		[]string{"/115open=>/volume1/CloudNAS/CloudDrive/115open"},
	)
	if len(paths) != 1 {
		t.Fatalf("expected one direct override path, got %#v", paths)
	}
	want := "/volume1/CloudNAS/CloudDrive/115open/上传下载/sample.zip"
	if paths[0] != want {
		t.Fatalf("unexpected direct override path: got %q want %q", paths[0], want)
	}
}

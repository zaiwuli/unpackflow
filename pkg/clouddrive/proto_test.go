package clouddrive

import "testing"

func TestParseMounts(t *testing.T) {
	var mount []byte
	putString(&mount, 1, "/mnt/cd2")
	putString(&mount, 2, "/115open")
	putBool(&mount, 9, true)
	var reply []byte
	putBytes(&reply, 1, mount)
	mounts, err := parseMounts(reply)
	if err != nil || len(mounts) != 1 {
		t.Fatalf("parse mounts: %v %#v", err, mounts)
	}
	if mounts[0].MountPath != "/mnt/cd2" || mounts[0].SourceDir != "/115open" || !mounts[0].IsMounted {
		t.Fatalf("unexpected mount: %#v", mounts[0])
	}
}

func TestParsePushChange(t *testing.T) {
	var change []byte
	putVarint(&change, uint64(1<<3))
	putVarint(&change, 0)
	putString(&change, 3, "/115open/Movies/movie.rar")
	var push []byte
	putVarint(&push, uint64(1<<3))
	putVarint(&push, 4)
	putBytes(&push, 5, change)
	parsed, ok, err := parsePush(push)
	if err != nil || !ok || parsed.Path != "/115open/Movies/movie.rar" || parsed.Type != 0 {
		t.Fatalf("unexpected push: ok=%v err=%v change=%#v", ok, err, parsed)
	}
}

func TestMonitorPathMappingDoesNotRequireMountAPI(t *testing.T) {
	paths := MapCloudPathWithOverrides("/115open/上传下载/new.zip", nil, []string{"/115open=>/volume1/CloudNAS/CloudDrive/115open"})
	if len(paths) != 1 || paths[0] != "/volume1/CloudNAS/CloudDrive/115open/上传下载/new.zip" {
		t.Fatalf("direct override mapping failed without mounts: %#v", paths)
	}
}

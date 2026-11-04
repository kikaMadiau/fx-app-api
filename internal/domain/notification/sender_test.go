package notification

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestOutboxSenderRestrictsMessageAndDirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ignore les droits Unix (os.Stat renvoie toujours 666/777)")
	}
	directory := t.TempDir()
	sender := &OutboxSender{Dir: directory}
	path, err := sender.Send(Message{Channel: ChannelEmail, Template: TemplatePasswordReset, To: "user@example.test", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm()&0o077 != 0 {
		t.Errorf("outbox file permissions = %o, must not grant group or other access", fileInfo.Mode().Perm())
	}
	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm()&0o077 != 0 {
		t.Errorf("outbox directory permissions = %o, must not grant group or other access", directoryInfo.Mode().Perm())
	}
}

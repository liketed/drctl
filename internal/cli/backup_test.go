package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liketed/dreamrouter-go/fakerouter"
	"github.com/liketed/dreamrouter-go/unifi"
)

// drctlIn runs drctl with the given standard input.
func drctlIn(t *testing.T, r *fakerouter.Router, stdin string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Env{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut, Insecure: true,
		Getenv: func(k string) string {
			switch k {
			case "DREAMROUTER_HOST":
				return r.Host()
			case "UNIFI_PASS":
				return fakerouter.Password
			}
			return ""
		},
		newClient: func(cfg unifi.Config) (*unifi.Client, error) {
			cfg.RetryIntervals = []time.Duration{20 * time.Millisecond}
			return unifi.New(cfg)
		},
	}
	code := Run(context.Background(), args, env)
	return result{code, out.String(), errOut.String()}
}

func fastRestoreWait(t *testing.T) {
	old := restoreWait
	restoreWait.interval, restoreWait.timeout, restoreWait.settle = 10*time.Millisecond, 2*time.Second, 0
	t.Cleanup(func() { restoreWait = old })
}

func fixedNow(t *testing.T) {
	old := now
	now = func() time.Time { return time.Date(2026, 10, 3, 20, 32, 0, 0, time.Local) }
	t.Cleanup(func() { now = old })
}

func TestBackupListDownloadDelete(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	fixedNow(t)
	dir := t.TempDir()
	t.Chdir(dir)
	r.PutAutoBackup(fakerouter.AutoBackup{Filename: "autobackup_10.6.101_20260831.unf", Version: "10.6.101",
		Time: time.Date(2026, 8, 31, 23, 30, 0, 0, time.UTC), Data: []byte("august")})
	r.PutAutoBackup(fakerouter.AutoBackup{Filename: "autobackup_10.6.106_20260930.unf", Version: "10.6.106",
		Time: time.Date(2026, 9, 30, 23, 30, 0, 0, time.UTC), Data: []byte("september")})

	res := drctl(t, r, "backup", "list")
	res.ok(t).says(t, "10.6.101", "autobackup_10.6.101_20260831.unf", "10.6.106", "9 B")
	if !strings.Contains(res.stderr, "2 automatic backups") {
		t.Fatalf("summary: %q", res.stderr)
	}

	// A new backup, saved privately under a dated name.
	drctl(t, r, "backup", "download").ok(t).says(t, "saved new backup to dreamrouter-2026-10-03-2032-10.6.106.unf", "keep it private")
	info, err := os.Stat("dreamrouter-2026-10-03-2032-10.6.106.unf")
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup file: %v %v", info, err)
	}
	if data, _ := os.ReadFile("dreamrouter-2026-10-03-2032-10.6.106.unf"); !bytes.Equal(data, r.Backup()) {
		t.Fatal("saved backup differs from the router's")
	}
	drctl(t, r, "backup", "download").fails(t, 1, "already exists; use --force")
	drctl(t, r, "backup", "download", "--force").ok(t)

	drctl(t, r, "backup", "download", "--latest", "sept.unf").ok(t).says(t, "saved automatic backup from", "to sept.unf", "Network 10.6.106")
	if data, _ := os.ReadFile("sept.unf"); string(data) != "september" {
		t.Fatalf("latest: %q", data)
	}
	drctl(t, r, "backup", "download", "--auto", "autobackup_10.6.101_20260831.unf", "aug.unf").ok(t)
	if data, _ := os.ReadFile("aug.unf"); string(data) != "august" {
		t.Fatalf("auto: %q", data)
	}
	drctl(t, r, "backup", "download", "--auto", "nope.unf", "x.unf").fails(t, 1, `no automatic backup named "nope.unf"`)
	drctl(t, r, "backup", "download", "--auto", "a", "--latest").fails(t, 2, "only one of --auto and --latest")

	drctl(t, r, "backup", "delete", "autobackup_10.6.101_20260831.unf", "--dry-run").ok(t).says(t, "would delete automatic backup")
	if len(r.AutoBackups()) != 2 {
		t.Fatal("--dry-run deleted")
	}
	drctl(t, r, "backup", "delete", "autobackup_10.6.101_20260831.unf").ok(t).says(t, "deleted automatic backup autobackup_10.6.101_20260831.unf")
	if len(r.AutoBackups()) != 1 {
		t.Fatal("not deleted")
	}
	drctl(t, r, "backup", "delete", "autobackup_10.6.101_20260831.unf").fails(t, 1, "no automatic backup named")
}

func TestBackupSchedule(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	drctl(t, r, "backup", "schedule").ok(t).says(t, "automatic backups: monthly on the 1st at 00:30 (time zone Europe/Dublin, settings only)")
	drctl(t, r, "backup", "schedule", "--weekly", "--dry-run").ok(t).says(t, "would change automatic backups from monthly on the 1st at 00:30 to weekly on Monday at 00:30")
	if r.BackupSchedule()["autobackup_cron_expr"] != "30 0 1 * *" {
		t.Fatal("--dry-run changed the schedule")
	}
	drctl(t, r, "backup", "schedule", "--daily", "--at", "03:15").ok(t).says(t, "to daily at 03:15")
	if s := r.BackupSchedule(); s["autobackup_cron_expr"] != "15 3 * * *" || s["autobackup_timezone"] != "Europe/Dublin" {
		t.Fatalf("stored: %v", s)
	}
	drctl(t, r, "backup", "schedule", "--at", "04:00").ok(t).says(t, "to daily at 04:00")
	drctl(t, r, "backup", "schedule", "--history", "7").ok(t)
	drctl(t, r, "backup", "schedule").ok(t).says(t, "daily at 04:00", "with 7 days of statistics")
	drctl(t, r, "backup", "schedule", "--off").ok(t).says(t, "to off")
	if r.BackupSchedule()["autobackup_enabled"] != false {
		t.Fatal("not turned off")
	}
	drctl(t, r, "backup", "schedule", "--monthly", "--at", "00:30", "--history", "0").ok(t).says(t, "to monthly on the 1st at 00:30")
	drctl(t, r, "backup", "schedule", "--monthly").ok(t).says(t, "already monthly on the 1st at 00:30")
	drctl(t, r, "backup", "schedule", "--daily", "--weekly").fails(t, 2, "only one of")
	drctl(t, r, "backup", "schedule", "--at", "25:00").fails(t, 2, "must be a time such as 00:30")
}

func TestBackupRestore(t *testing.T) {
	r := fakerouter.New()
	defer r.Close()
	fastRestoreWait(t)
	fixedNow(t)
	dir := t.TempDir()
	backup := filepath.Join(dir, "before.unf")
	if err := os.WriteFile(backup, r.Backup(), 0o600); err != nil {
		t.Fatal(err)
	}
	// The change to undo.
	r.PutDNS(fakerouter.DNSRecord{RecordType: "A", Key: "restore-test.home.internal", Value: "192.168.1.250", Enabled: true})

	drctl(t, r, "backup", "restore", backup, "--dry-run").ok(t).
		says(t, "backup made", "by Network 10.6.106", "checked by the router", "would replace all of Dream Router 7's network settings")
	if r.Restores() != 0 {
		t.Fatal("--dry-run restored")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatal("--dry-run saved a safety backup")
	}

	// The wrong name (or none) restores nothing.
	drctlIn(t, r, "Dream Router\n", "backup", "restore", backup).fails(t, 1, "not restored: the name didn't match")
	drctlIn(t, r, "", "backup", "restore", backup).fails(t, 1, "not restored")
	if r.Restores() != 0 || len(r.DNS()) != 1 {
		t.Fatal("restored without confirmation")
	}
	// Each attempt saved its own safety backup without blocking the next.
	if _, err := os.Stat(filepath.Join(dir, "dreamrouter-2026-10-03-2032-10.6.106-before-restore-2.unf")); err != nil {
		t.Fatal("second safety backup missing:", err)
	}
	safetyPath := filepath.Join(dir, "dreamrouter-2026-10-03-2032-10.6.106-before-restore-3.unf")

	res := drctlIn(t, r, "Dream Router 7\n", "backup", "restore", backup)
	res.ok(t).says(t, "saved the current settings to "+safetyPath+" first",
		"Type the router's name (Dream Router 7) to restore:", "restored Dream Router 7 from "+backup)
	if r.Restores() != 1 || len(r.DNS()) != 0 {
		t.Fatalf("change not reverted: %v", r.DNS())
	}
	// The safety backup holds the state before the restore (with the change).
	safety, _ := os.ReadFile(safetyPath)
	if !strings.Contains(string(safety), "restore-test.home.internal") {
		t.Fatal("safety backup doesn't hold the state before the restore")
	}
	if info, _ := os.Stat(safetyPath); info.Mode().Perm() != 0o600 {
		t.Fatal("safety backup is readable by others")
	}

	// --yes skips the question; --no-safety-backup skips the safety backup.
	r.PutDNS(fakerouter.DNSRecord{RecordType: "A", Key: "again.home.internal", Value: "192.168.1.251", Enabled: true})
	res = drctl(t, r, "backup", "restore", backup, "--yes", "--no-safety-backup")
	res.ok(t).says(t, "restored Dream Router 7")
	if strings.Contains(res.stdout, "saved the current settings") || r.Restores() != 2 || len(r.DNS()) != 0 {
		t.Fatalf("--yes --no-safety-backup: %s / restores %d", res.stdout, r.Restores())
	}

	junk := filepath.Join(dir, "junk.unf")
	os.WriteFile(junk, []byte("not a backup"), 0o600)
	drctl(t, r, "backup", "restore", junk, "--yes").fails(t, 1, "is not a backup of the Network application (the router rejected it)")
	drctl(t, r, "backup", "restore", filepath.Join(dir, "missing.unf"), "--yes").fails(t, 1, "no such file")
	if r.Restores() != 2 {
		t.Fatal("restored a bad file")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"10.6.106", "10.6.106", 0}, {"10.4.57", "10.6.106", -1}, {"10.10.1", "10.9.9", 1}, {"10.6", "10.6.0", 0}} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

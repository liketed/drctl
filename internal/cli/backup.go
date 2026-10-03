package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/liketed/dreamrouter-go/unifi"
)

// restoreWait is how often and how long "backup restore" waits for the
// Network application to come back, and how long it keeps watching for the
// restart to begin before accepting an answer as "back". Replaced in tests.
var restoreWait = struct{ interval, timeout, settle time.Duration }{3 * time.Second, 5 * time.Minute, 30 * time.Second}

func backupList(ctx context.Context, c *command) error {
	fs := c.flags("[--format table|csv|json]")
	format := fs.String("format", "table", "output format: table, csv or json")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	list, err := api.ListBackups(ctx)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, b := range list {
		if *format == "table" {
			rows = append(rows, []string{b.Time.Local().Format("2006-01-02 15:04"), b.Version, bytesText(float64(b.Size)), b.Filename})
		} else {
			rows = append(rows, []string{b.Time.UTC().Format(time.RFC3339), b.Version, fmt.Sprint(b.Size), b.Filename})
		}
	}
	header := []string{"made", "version", "size", "file"}
	if *format != "table" {
		header = []string{"made", "version", "size_bytes", "file"}
	}
	if err := output(c.env.Stdout, *format, header, rows); err != nil {
		return err
	}
	if *format == "table" {
		fmt.Fprintf(c.env.Stderr, "%d automatic backups\n", len(list))
	}
	return nil
}

// defaultBackupName is e.g. "dreamrouter-2026-10-03-2032-10.6.106.unf".
func defaultBackupName(version, suffix string) string {
	return fmt.Sprintf("dreamrouter-%s-%s%s.unf", now().Format("2006-01-02-1504"), version, suffix)
}

// saveBackup writes a backup readable only by the user, refusing to
// overwrite a file unless force is set.
func saveBackup(path string, data []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists; use --force to overwrite it", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// freeName returns path, or path with "-2", "-3", ... before the extension
// if it exists, so safety backups never overwrite or block each other.
func freeName(path string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; ; i++ {
		p := path
		if i > 1 {
			p = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			return p
		}
	}
}

func backupDownload(ctx context.Context, c *command) error {
	fs := c.flags("[FILE] [--auto NAME | --latest] [--history DAYS] [--force]")
	auto := fs.String("auto", "", "download this automatic backup (see \"drctl backup list\") instead of making a new one")
	latest := fs.Bool("latest", false, "download the newest automatic backup")
	history := fs.Int("history", 0, "days of statistics to include in a new backup (0: settings only)")
	force := fs.Bool("force", false, "overwrite FILE if it exists")
	args, err := c.parse(0, 1)
	if err != nil {
		return err
	}
	if *auto != "" && *latest {
		return usagef("use only one of --auto and --latest")
	}
	if *history < 0 {
		return usagef("--history must be 0 or more days")
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	var data []byte
	var version, what string
	switch {
	case *auto != "" || *latest:
		list, err := api.ListBackups(ctx)
		if err != nil {
			return err
		}
		var pick *unifi.Backup
		for i := range list {
			if (*latest && i == len(list)-1) || list[i].Filename == *auto {
				pick = &list[i]
			}
		}
		if pick == nil {
			if *latest {
				return fmt.Errorf("the router has no automatic backups")
			}
			return fmt.Errorf("no automatic backup named %q; see \"drctl backup list\"", *auto)
		}
		if data, err = api.DownloadAutoBackup(ctx, pick.Filename); err != nil {
			return err
		}
		version, what = pick.Version, "automatic backup from "+pick.Time.Local().Format("2006-01-02 15:04")
	default:
		st, err := api.GetStatus(ctx)
		if err != nil {
			return err
		}
		if data, err = api.DownloadBackup(ctx, *history); err != nil {
			return err
		}
		version, what = st.NetworkVersion, "new backup"
	}
	path := defaultBackupName(version, "")
	if len(args) == 1 {
		path = args[0]
	}
	if path == "-" {
		_, err := c.env.Stdout.Write(data)
		return err
	}
	if err := saveBackup(path, data, *force); err != nil {
		return err
	}
	c.out("saved %s to %s (%s, Network %s); it contains passwords and keys, keep it private", what, path, bytesText(float64(len(data))), version)
	return nil
}

func backupDelete(ctx context.Context, c *command) error {
	c.flags("NAME")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	list, err := api.ListBackups(ctx)
	if err != nil {
		return err
	}
	for _, b := range list {
		if b.Filename == args[0] {
			if !c.dryRun {
				if err := api.DeleteAutoBackup(ctx, b.Filename); err != nil {
					return err
				}
			}
			c.out("%s automatic backup %s (made %s)", c.verb("deleted", "delete"), b.Filename, b.Time.Local().Format("2006-01-02 15:04"))
			return nil
		}
	}
	return fmt.Errorf("no automatic backup named %q; see \"drctl backup list\"", args[0])
}

// scheduleText describes a cron schedule made by "backup schedule", e.g.
// "monthly on the 1st at 00:30"; other expressions are shown as is.
func scheduleText(s unifi.BackupSchedule) string {
	if !s.Enabled {
		return "off"
	}
	f := strings.Fields(s.Cron)
	if len(f) != 5 {
		return "cron " + s.Cron
	}
	m, errM := strconv.Atoi(f[0])
	h, errH := strconv.Atoi(f[1])
	if errM != nil || errH != nil {
		return "cron " + s.Cron
	}
	at := fmt.Sprintf("%02d:%02d", h, m)
	days := map[string]string{"0": "Sunday", "1": "Monday", "2": "Tuesday", "3": "Wednesday", "4": "Thursday", "5": "Friday", "6": "Saturday", "7": "Sunday"}
	switch {
	case f[2] == "*" && f[3] == "*" && f[4] == "*":
		return "daily at " + at
	case f[2] == "*" && f[3] == "*" && days[f[4]] != "":
		return "weekly on " + days[f[4]] + " at " + at
	case f[2] == "1" && f[3] == "*" && f[4] == "*":
		return "monthly on the 1st at " + at
	}
	return "cron " + s.Cron
}

func backupSchedule(ctx context.Context, c *command) error {
	fs := c.flags("[--daily | --weekly | --monthly | --off] [--at HH:MM] [--history DAYS]")
	daily := fs.Bool("daily", false, "make an automatic backup every day")
	weekly := fs.Bool("weekly", false, "every Monday")
	monthly := fs.Bool("monthly", false, "on the 1st of each month")
	off := fs.Bool("off", false, "stop making automatic backups")
	at := fs.String("at", "", "time of day, e.g. 00:30 (default: keep the current time)")
	history := fs.Int("history", -1, "days of statistics to include (0: settings only; default: keep)")
	if _, err := c.parse(0, 0); err != nil {
		return err
	}
	if countTrue(*daily, *weekly, *monthly, *off) > 1 {
		return usagef("use only one of --daily, --weekly, --monthly and --off")
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	cur, err := api.GetBackupSchedule(ctx)
	if err != nil {
		return err
	}
	changing := *daily || *weekly || *monthly || *off || *at != "" || *history >= 0
	if !changing {
		c.out("automatic backups: %s (time zone %s, %s)", scheduleText(cur), orDash(cur.Timezone), historyText(cur.HistoryDays))
		return nil
	}
	next := cur
	h, m := 0, 30
	if f := strings.Fields(cur.Cron); len(f) == 5 {
		if v, err := strconv.Atoi(f[1]); err == nil {
			h = v
		}
		if v, err := strconv.Atoi(f[0]); err == nil {
			m = v
		}
	}
	if *at != "" {
		t, err := time.Parse("15:04", *at)
		if err != nil {
			return usagef("--at %q must be a time such as 00:30", *at)
		}
		h, m = t.Hour(), t.Minute()
	}
	switch {
	case *daily:
		next.Cron = fmt.Sprintf("%d %d * * *", m, h)
	case *weekly:
		next.Cron = fmt.Sprintf("%d %d * * 1", m, h)
	case *monthly:
		next.Cron = fmt.Sprintf("%d %d 1 * *", m, h)
	case *at != "":
		f := strings.Fields(cur.Cron)
		if len(f) != 5 {
			f = []string{"", "", "1", "*", "*"}
		}
		next.Cron = fmt.Sprintf("%d %d %s %s %s", m, h, f[2], f[3], f[4])
	}
	next.Enabled = !*off
	if *history >= 0 {
		next.HistoryDays = *history
	}
	if next == cur {
		c.out("automatic backups are already %s", scheduleText(cur))
		return nil
	}
	if !c.dryRun {
		if next, err = api.SetBackupSchedule(ctx, next); err != nil {
			return err
		}
	}
	c.out("%s automatic backups from %s to %s", c.verb("changed", "change"), scheduleText(cur), scheduleText(next))
	return nil
}

func historyText(days int) string {
	if days == 0 {
		return "settings only"
	}
	return fmt.Sprintf("with %d days of statistics", days)
}

// compareVersions compares dotted versions numerically: -1, 0 or 1.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func backupRestore(ctx context.Context, c *command) error {
	fs := c.flags("FILE [--yes] [--no-safety-backup]")
	yes := fs.Bool("yes", false, "don't ask for confirmation")
	noSafety := fs.Bool("no-safety-backup", false, "don't save a backup of the current settings first")
	args, err := c.parse(1, 1)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	api, err := c.connect()
	if err != nil {
		return err
	}
	st, err := api.GetStatus(ctx)
	if err != nil {
		return err
	}

	// The router checks the file and keeps it ready; nothing changes yet.
	staged, err := api.UploadBackup(ctx, data, filepath.Base(args[0]))
	if err != nil {
		if unifi.HasCode(err, unifi.CodeInvalidBackup) {
			return fmt.Errorf("%s is not a backup of the Network application (the router rejected it)", args[0])
		}
		return err
	}
	made := "unknown date"
	if !staged.Time.IsZero() {
		made = staged.Time.Local().Format("2006-01-02 15:04")
	}
	if compareVersions(staged.Version, st.NetworkVersion) > 0 {
		return fmt.Errorf("%s was made by Network %s, newer than the router's %s; update the router first", args[0], staged.Version, st.NetworkVersion)
	}
	c.out("%s: backup made %s by Network %s (the router runs %s), checked by the router", args[0], made, staged.Version, st.NetworkVersion)
	if compareVersions(staged.Version, st.NetworkVersion) < 0 {
		c.warn("the backup is from an older Network version (%s); the router will convert it", staged.Version)
	}
	if c.dryRun {
		c.out("would replace all of %s's network settings with this backup; the Network application would restart (about a minute)", st.Name)
		return nil
	}

	if !*noSafety {
		cur, err := api.DownloadBackup(ctx, 0)
		if err != nil {
			return fmt.Errorf("saving a backup of the current settings first: %w (use --no-safety-backup to skip)", err)
		}
		safety := freeName(filepath.Join(filepath.Dir(args[0]), defaultBackupName(st.NetworkVersion, "-before-restore")))
		if err := saveBackup(safety, cur, false); err != nil {
			return fmt.Errorf("saving a backup of the current settings first: %w", err)
		}
		c.out("saved the current settings to %s first", safety)
	}

	if !*yes {
		fmt.Fprintf(c.env.Stdout, "This replaces ALL of %s's network settings (networks, Wi-Fi, DNS, reservations, port forwards, ...)\n"+
			"with the backup's. The Network application restarts; routing carries on.\nType the router's name (%s) to restore: ", st.Name, st.Name)
		line, _ := bufio.NewReader(c.env.stdin()).ReadString('\n')
		if strings.TrimSpace(line) != st.Name {
			return fmt.Errorf("not restored: the name didn't match")
		}
	}
	start := time.Now()
	if err := api.RestoreBackup(ctx, staged.ID); err != nil {
		return err
	}
	c.out("restoring; waiting for the Network application to restart...")
	deadline := start.Add(restoreWait.timeout)
	sawDown := false
	for {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := api.GetStatus(wctx)
		cancel()
		if err != nil {
			sawDown = true
		} else if sawDown || time.Since(start) >= restoreWait.settle {
			c.out("restored %s from %s; the Network application is back after %s", st.Name, args[0], time.Since(start).Round(time.Second))
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("restore sent, but the Network application isn't answering after %s; check the web UI", restoreWait.timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(restoreWait.interval):
		}
	}
}

// Package cli implements the drctl command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/liketed/dreamrouter-go/unifi"
)

// Version is set at build time with -ldflags "-X github.com/liketed/drctl/internal/cli.Version=1.2.3".
var Version = "dev"

// Env is everything a command touches outside the process, so tests can run
// commands in-process.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	// ReadPassword prompts for a password; nil means no terminal is available.
	ReadPassword func(prompt string) (string, error)
	// Insecure skips TLS verification (the router's certificate is self-signed).
	Insecure bool
	// newClient is replaced in tests to shorten login retry waits.
	newClient func(unifi.Config) (*unifi.Client, error)
}

// SystemEnv is the environment of a real drctl process.
func SystemEnv() Env {
	env := Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Insecure: true}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		env.ReadPassword = func(prompt string) (string, error) {
			fmt.Fprint(os.Stderr, prompt)
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			return string(b), err
		}
	}
	return env
}

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// usageError is a mistake in the command line; it exits with code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

const mainUsage = `drctl manages a UniFi Dream Router 7's static DNS records, DHCP reservations, clients, port forwards, backups and network settings.

Usage:
  drctl dns  list   [--type T] [--name N] [--static] [--format table|csv|json]
  drctl dns  add    NAME VALUE [--type A] [--ttl N] [--priority N] [--weight N] [--port N] [--disabled] [--append]
  drctl dns  delete NAME [--type T] [--value V] [--all]
  drctl dns  import FILE [--delete]

  drctl dhcp list   [--format table|csv|json]
  drctl dhcp add    MAC IP [--name NAME] [--network NET]
  drctl dhcp delete MAC [--forget]
  drctl dhcp import FILE [--delete]

  drctl host list   [--format table|csv|json]
  drctl host add    NAME IP --mac MAC [--network NET] [--device-name NAME]
  drctl host delete NAME [--keep-reservation]

  drctl leases list    [--network NET] [--format table|csv|json]
  drctl leases reserve IP|MAC [--name NAME] [--dns-name NAME]

  drctl clients list    [--offline | --all | --blocked] [--wired | --wifi] [--days N] [--format table|csv|json]
  drctl clients show    MAC|IP|NAME
  drctl clients name    MAC NAME
  drctl clients note    MAC TEXT
  drctl clients block   MAC
  drctl clients unblock MAC
  drctl clients forget  MAC

  drctl portforward list    [--format table|csv|json]
  drctl portforward add     NAME PORT IP[:PORT] [--proto tcp|udp|both] [--from CIDR] [--disabled] [--log]
  drctl portforward delete  NAME
  drctl portforward enable  NAME
  drctl portforward disable NAME

  drctl network list   [--format table|csv|json]
  drctl network show   [NETWORK] [--format table|csv|json]
  drctl network boot   [NETWORK] --server IP --file NAME [--tftp-server HOST | --no-tftp]
  drctl network boot   [NETWORK] --off [--no-tftp]

  drctl backup list      [--format table|csv|json]
  drctl backup download  [FILE] [--auto NAME | --latest] [--history DAYS] [--force]
  drctl backup schedule  [--daily | --weekly | --monthly | --off] [--at HH:MM] [--history DAYS]
  drctl backup delete    NAME
  drctl backup restore   FILE [--yes] [--no-safety-backup]

  drctl ssh     [--format text|json]          show SSH to the router and to adopted devices
  drctl ssh router  on|off
  drctl ssh devices on|off

  drctl status  [--format text|json]   the router's versions, internet connection, load, clients and firmware

  drctl version       show the version, source commit and commit date

Options for every command:
  --host HOST          router address (default $DREAMROUTER_HOST, then 192.168.1.1)
  --user USER          local UniFi OS account (default $DREAMROUTER_USERNAME or $UNIFI_USER, then admin)
  --site SITE          UniFi Network site (default "default")
  --dry-run            show what would change without changing anything
  --login-retry DUR    keep retrying for DUR when the router's login limit is reached (default 2m, 0 to fail at once)

The password is read from $DREAMROUTER_PASSWORD or $UNIFI_PASS, otherwise prompted for.

Run "drctl COMMAND SUBCOMMAND --help" for details, e.g. "drctl dhcp add --help".
`

// Run runs drctl with the given arguments (without the program name) and
// returns the exit code.
func Run(ctx context.Context, args []string, env Env) int {
	if env.newClient == nil {
		env.newClient = unifi.New
	}
	err := run(ctx, args, &env)
	var ue usageError
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.As(err, &ue):
		fmt.Fprintf(env.Stderr, "drctl: %s\nRun \"drctl --help\" for usage.\n", ue.msg)
		return exitUsage
	default:
		fmt.Fprintf(env.Stderr, "drctl: %s\n", err)
		return exitError
	}
}

func run(ctx context.Context, args []string, env *Env) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(env.Stdout, mainUsage)
		return nil
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintln(env.Stdout, versionString())
		return nil
	}
	if args[0] == "status" {
		return statusCmd(ctx, &command{env: env, name: "status", args: args[1:]})
	}
	commands := map[string]map[string]func(context.Context, *command) error{
		"dns":     {"list": dnsList, "add": dnsAdd, "delete": dnsDelete, "import": dnsImport},
		"dhcp":    {"list": dhcpList, "add": dhcpAdd, "delete": dhcpDelete, "import": dhcpImport},
		"host":    {"list": hostList, "add": hostAdd, "delete": hostDelete},
		"network": {"list": networkList, "show": networkShow, "boot": networkBoot},
		"leases":  {"list": leasesList, "reserve": leasesReserve},
		"clients": {"list": clientsList, "show": clientsShow, "name": clientsName, "note": clientsNote,
			"block": clientsBlock, "unblock": clientsUnblock, "forget": clientsForget},
		"ssh": {"show": sshShow, "router": sshRouter, "devices": sshDevices},
		"backup": {"list": backupList, "download": backupDownload, "delete": backupDelete,
			"schedule": backupSchedule, "restore": backupRestore},
		"portforward": {"list": portforwardList, "add": portforwardAdd, "delete": portforwardDelete,
			"enable": portforwardEnable, "disable": portforwardDisable},
	}
	group, ok := commands[args[0]]
	if !ok {
		return usagef("unknown command %q", args[0])
	}
	if args[0] == "ssh" && (len(args) == 1 || len(args[1]) > 0 && args[1][0] == '-') {
		args = append([]string{"ssh", "show"}, args[1:]...) // "drctl ssh" shows both settings
	}
	if len(args) < 2 {
		return usagef("%q needs a subcommand", args[0])
	}
	fn, ok := group[args[1]]
	if !ok {
		return usagef("unknown subcommand %q for %s", args[1], args[0])
	}
	return fn(ctx, &command{env: env, name: args[0] + " " + args[1], args: args[2:]})
}

// command is one invocation: its flags, positional arguments and connection.
type command struct {
	env  *Env
	name string
	args []string

	fs         *flag.FlagSet
	usage      string
	host, user string
	site       string
	dryRun     bool
	loginRetry time.Duration

	api *unifi.Client
}

// flags creates the flag set with the global options added.
func (c *command) flags(usage string) *flag.FlagSet {
	c.usage = usage
	c.fs = flag.NewFlagSet("drctl "+c.name, flag.ContinueOnError)
	c.fs.SetOutput(c.env.Stderr)
	c.fs.Usage = func() {
		fmt.Fprintf(c.env.Stderr, "Usage: drctl %s %s\n\nOptions:\n", c.name, usage)
		c.fs.PrintDefaults()
	}
	c.fs.StringVar(&c.host, "host", "", "router address (default $DREAMROUTER_HOST, then 192.168.1.1)")
	c.fs.StringVar(&c.user, "user", "", "local UniFi OS account (default $DREAMROUTER_USERNAME or $UNIFI_USER, then admin)")
	c.fs.StringVar(&c.site, "site", "default", "UniFi Network site")
	c.fs.BoolVar(&c.dryRun, "dry-run", false, "show what would change without changing anything")
	c.fs.DurationVar(&c.loginRetry, "login-retry", 2*time.Minute, "keep retrying this long when the router's login limit is reached")
	return c.fs
}

// parse parses flags, which may appear before, between or after positional
// arguments, and returns the positional arguments.
func (c *command) parse(minArgs, maxArgs int) ([]string, error) {
	var positional []string
	args := c.args
	for {
		if err := c.fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usageError{err.Error()}
		}
		if c.fs.NArg() == 0 {
			break
		}
		positional = append(positional, c.fs.Arg(0))
		args = c.fs.Args()[1:]
	}
	if len(positional) < minArgs || len(positional) > maxArgs {
		return nil, usagef("wrong number of arguments; usage: drctl %s %s", c.name, c.usage)
	}
	return positional, nil
}

// connect creates the API client (it logs in on first use).
func (c *command) connect() (*unifi.Client, error) {
	if c.api != nil {
		return c.api, nil
	}
	env := c.env
	host := firstNonEmpty(c.host, env.Getenv("DREAMROUTER_HOST"), "192.168.1.1")
	user := firstNonEmpty(c.user, env.Getenv("DREAMROUTER_USERNAME"), env.Getenv("UNIFI_USER"), "admin")
	password := firstNonEmpty(env.Getenv("DREAMROUTER_PASSWORD"), env.Getenv("UNIFI_PASS"))
	if password == "" {
		if env.ReadPassword == nil {
			return nil, errors.New("no password: set DREAMROUTER_PASSWORD or UNIFI_PASS")
		}
		p, err := env.ReadPassword(fmt.Sprintf("Password for %s@%s: ", user, host))
		if err != nil {
			return nil, fmt.Errorf("reading password: %w", err)
		}
		password = p
	}
	api, err := env.newClient(unifi.Config{
		Host: host, Site: c.site, Username: user, Password: password,
		InsecureSkipVerify: env.Insecure,
		LoginRetryTimeout:  c.loginRetry,
		Logf: func(_ context.Context, msg string) {
			fmt.Fprintf(env.Stderr, "drctl: %s\n", msg)
		},
	})
	if err != nil {
		return nil, err
	}
	c.api = api
	return api, nil
}

// out prints a result line; with --dry-run, "created" becomes "would create".
func (c *command) out(format string, args ...any) {
	fmt.Fprintf(c.env.Stdout, format+"\n", args...)
}

func (c *command) warn(format string, args ...any) {
	fmt.Fprintf(c.env.Stderr, "drctl: warning: "+format+"\n", args...)
}

// verb returns the past tense normally and "would <present>" with --dry-run.
func (c *command) verb(past, present string) string {
	if c.dryRun {
		return "would " + present
	}
	return past
}

func (e *Env) stdin() io.Reader {
	if e.Stdin == nil {
		return strings.NewReader("")
	}
	return e.Stdin
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

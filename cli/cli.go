// Package cli implements the headless interface of the product. It runs on
// the same service layer as the desktop app, so a request behaves identically
// whether it is sent from the window or from a shell.
package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"yourpm/core/brand"
	"yourpm/core/model"
	"yourpm/core/service"
	syncl "yourpm/core/sync"
)

const usage = `%s %s — native, private API client

Usage:
  {{cmd}}                     open the desktop app
  {{cmd}} version             print the version
  {{cmd}} workspaces          list local workspaces
  {{cmd}} envs                list environments in a workspace
  {{cmd}} ls                  list requests and collections
  {{cmd}} run [flags]         send a saved or ad-hoc request
  {{cmd}} run-collection [flags] <name>
                              run every request in a collection or folder
  {{cmd}} authorize [flags]   connect an OAuth 2.0 authorization-code request
  {{cmd}} backup [flags]      export the current workspace to a JSON file
  {{cmd}} restore <file>      restore a workspace backup as a new workspace
  {{cmd}} sync                push and pull the current workspace
  {{cmd}} devices             list signed-in sync devices
  {{cmd}} revoke-device <id>  revoke another device
  {{cmd}} import <file>       import a Postman v2.1 collection
  {{cmd}} export <name>       export a collection or environment
  {{cmd}} history             show recent requests

Run "{{cmd}} <command> -h" for the flags of a command.

Environment:
  YOURPM_DATA                data directory (default: OS config dir/{{cmd}})
  YOURPM_PASSPHRASE          vault passphrase, needed when the vault is enabled
  YOURPM_BACKUP_PASSPHRASE   optional passphrase for encrypted backups
`

// usageText renders the help text for the current brand.
func usageText() string {
	return strings.ReplaceAll(fmt.Sprintf(usage, brand.Name, brand.Version), "{{cmd}}", brand.Slug)
}

// Main dispatches a command line and returns the process exit code.
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "cli: missing command\n\n"+usageText())
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(usageText())
		return 0
	case "version", "--version", "-v":
		fmt.Printf("%s %s\n", brand.Name, brand.Version)
		return 0
	}

	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "workspaces":
		err = cmdWorkspaces(rest)
	case "envs", "environments":
		err = cmdEnvs(rest)
	case "ls", "list":
		err = cmdList(rest)
	case "run":
		err = cmdRun(rest)
	case "run-collection", "runner":
		err = cmdRunCollection(rest)
	case "authorize":
		err = cmdAuthorize(rest)
	case "backup":
		err = cmdBackup(rest)
	case "restore":
		err = cmdRestore(rest)
	case "sync":
		err = cmdSync(rest)
	case "devices":
		err = cmdDevices(rest)
	case "revoke-device":
		err = cmdRevokeDevice(rest)
	case "import":
		err = cmdImport(rest)
	case "export":
		err = cmdExport(rest)
	case "history":
		err = cmdHistory(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usageText())
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		var code exitCode
		if errors.As(err, &code) {
			return int(code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// exitCode lets commands choose a specific process status without printing
// an extra error line.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

/* ------------------------------------------------------------------ */
/* Shared state                                                       */
/* ------------------------------------------------------------------ */

type session struct {
	svc *service.Service
	ws  *model.Workspace
}

type commonFlags struct {
	dataDir string
	wsName  string
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.dataDir, "data-dir", "", "data directory (default: "+defaultDataDirHint()+")")
	fs.StringVar(&c.wsName, "workspace", "", "workspace name or id (default: the first one)")
}

func defaultDataDirHint() string {
	if v := os.Getenv("YOURPM_DATA"); v != "" {
		return v
	}
	dir, err := service.DefaultDataDir()
	if err != nil {
		return "OS config dir"
	}
	return dir
}

func openSession(c commonFlags) (*session, error) {
	svc, err := service.Open(c.dataDir)
	if err != nil {
		return nil, err
	}
	enabled, unlocked := svc.VaultStatus()
	if enabled && !unlocked {
		pass := os.Getenv("YOURPM_PASSPHRASE")
		if pass == "" {
			svc.Close()
			return nil, errors.New("the vault is locked; set YOURPM_PASSPHRASE to unlock it")
		}
		if err := svc.VaultUnlock(pass); err != nil {
			svc.Close()
			return nil, fmt.Errorf("could not unlock the vault: %w", err)
		}
	}
	list, err := svc.Store.ListWorkspaces()
	if err != nil {
		svc.Close()
		return nil, err
	}
	if len(list) == 0 {
		svc.Close()
		return nil, errors.New("no workspaces found; open the desktop app once to create one")
	}
	ws := list[0]
	if c.wsName != "" {
		ws = findWorkspace(list, c.wsName)
		if ws == nil {
			svc.Close()
			return nil, fmt.Errorf("workspace %q not found", c.wsName)
		}
	}
	return &session{svc: svc, ws: ws}, nil
}

func (s *session) close() { _ = s.svc.Close() }

func findWorkspace(list []*model.Workspace, name string) *model.Workspace {
	for _, w := range list {
		if w.ID == name {
			return w
		}
	}
	for _, w := range list {
		if strings.EqualFold(w.Name, name) {
			return w
		}
	}
	return nil
}

func (s *session) entities() ([]*model.Entity, error) {
	return s.svc.Store.ListEntities(s.ws.ID, false)
}

func (s *session) findEntity(name string, kinds ...string) (*model.Entity, error) {
	list, err := s.entities()
	if err != nil {
		return nil, err
	}
	allowed := func(kind string) bool {
		if len(kinds) == 0 {
			return kind != model.KindEnvironment
		}
		for _, k := range kinds {
			if k == kind {
				return true
			}
		}
		return false
	}
	for _, e := range list {
		if e.ID == name && allowed(e.Kind) {
			return e, nil
		}
	}
	var matches []*model.Entity
	for _, e := range list {
		if allowed(e.Kind) && strings.EqualFold(e.Name, name) {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%q not found", name)
	case 1:
		return matches[0], nil
	}
	names := make([]string, len(matches))
	for i, e := range matches {
		names[i] = e.Kind + " " + e.ID
	}
	return nil, fmt.Errorf("%q is ambiguous (%s); use the id", name, strings.Join(names, ", "))
}

func (s *session) envID(name string) string {
	if name == "" {
		return s.svc.Store.Setting("ui.env." + s.ws.ID)
	}
	list, err := s.entities()
	if err != nil {
		return ""
	}
	for _, e := range list {
		if e.Kind != model.KindEnvironment {
			continue
		}
		if e.ID == name || strings.EqualFold(e.Name, name) {
			return e.ID
		}
	}
	return ""
}

/* ------------------------------------------------------------------ */
/* Listing                                                            */
/* ------------------------------------------------------------------ */

func cmdWorkspaces(args []string) error {
	fs := flag.NewFlagSet("workspaces", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	list, err := s.svc.Store.ListWorkspaces()
	if err != nil {
		return err
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	for _, w := range list {
		mark := " "
		if w.ID == s.ws.ID {
			mark = "*"
		}
		sync := "local"
		if w.Linked() {
			sync = w.Sync.Server
		}
		fmt.Printf("%s %-24s %s\n", mark, w.Name, sync)
	}
	return nil
}

func cmdEnvs(args []string) error {
	fs := flag.NewFlagSet("envs", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	list, err := s.entities()
	if err != nil {
		return err
	}
	active := s.envID("")
	n := 0
	for _, e := range list {
		if e.Kind != model.KindEnvironment {
			continue
		}
		env, _ := service.EnvironmentOf(e)
		mark := " "
		if e.ID == active {
			mark = "*"
		}
		fmt.Printf("%s %-24s %d variables\n", mark, e.Name, len(env.Variables))
		n++
	}
	if n == 0 {
		fmt.Println("no environments")
	}
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	var c commonFlags
	var kind string
	c.register(fs)
	fs.StringVar(&kind, "kind", "", "filter by kind: collection, folder, request, environment")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	list, err := s.entities()
	if err != nil {
		return err
	}
	children := map[string][]*model.Entity{}
	for _, e := range list {
		children[e.ParentID] = append(children[e.ParentID], e)
	}
	for parent := range children {
		service.SortEntities(children[parent])
	}
	var walk func(parent, indent string)
	walk = func(parent, indent string) {
		for _, e := range children[parent] {
			if kind != "" && e.Kind != kind {
				continue
			}
			line := indent + e.Name
			switch e.Kind {
			case model.KindRequest:
				req, _ := service.RequestOf(e)
				line += "\t" + strings.ToUpper(req.Method) + " " + req.URL
			case model.KindEnvironment:
				line += "\t(environment)"
			}
			fmt.Println(line)
			walk(e.ID, indent+"  ")
		}
	}
	walk("", "")
	return nil
}

/* ------------------------------------------------------------------ */
/* Run                                                                */
/* ------------------------------------------------------------------ */

type kvFlag []string

func (f *kvFlag) String() string { return strings.Join(*f, ", ") }
func (f *kvFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// reorderFlags moves options ahead of positional arguments. Go's flag package
// stops at the first non-flag argument, but "run POST URL -d body" is how
// people naturally type commands.
func reorderFlags(fs *flag.FlagSet, args []string) []string {
	var opts, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			rest = append(rest, a)
			continue
		}
		opts = append(opts, a)
		if strings.Contains(a, "=") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			opts = append(opts, args[i])
		}
	}
	return append(opts, rest...)
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var c commonFlags
	var (
		request  string
		envName  string
		body     string
		timeout  = 60
		output   string
		pretty   bool
		failFast bool
		failTest bool
		insecure bool
		headers  kvFlag
		vars     kvFlag
	)
	c.register(fs)
	fs.StringVar(&request, "request", "", "saved request name or id")
	fs.StringVar(&envName, "env", "", "environment name or id (default: active environment)")
	fs.StringVar(&body, "data", "", "raw request body")
	fs.StringVar(&body, "d", "", "raw request body (shorthand)")
	fs.IntVar(&timeout, "timeout", 60, "request timeout in seconds")
	fs.StringVar(&output, "output", "", "write the response body to a file")
	fs.StringVar(&output, "o", "", "write the response body to a file (shorthand)")
	fs.BoolVar(&pretty, "pretty", false, "pretty-print JSON response bodies")
	fs.BoolVar(&failFast, "fail", false, "exit with status 2 for HTTP 400 and above")
	fs.BoolVar(&failTest, "fail-tests", false, "exit with status 3 when a pm.test() fails")
	fs.BoolVar(&insecure, "insecure", false, "skip TLS certificate verification")
	fs.Var(&headers, "header", "extra header, NAME: VALUE (repeatable)")
	fs.Var(&headers, "H", "extra header, NAME: VALUE (repeatable)")
	fs.Var(&vars, "var", "variable override, name=value (repeatable)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}

	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()

	envID := s.envID(envName)
	if envName != "" && envID == "" {
		return fmt.Errorf("environment %q not found", envName)
	}

	overrides, err := parseVars(vars)
	if err != nil {
		return err
	}

	var req model.RequestData
	var reqID, collectionID string
	if request != "" {
		e, err := s.findEntity(request, model.KindRequest)
		if err != nil {
			return err
		}
		req, err = service.RequestOf(e)
		if err != nil {
			return err
		}
		reqID = e.ID
		collectionID = s.svc.CollectionIDFor(s.ws.ID, e.ID)
	} else {
		rest := fs.Args()
		if len(rest) == 0 {
			return errors.New("nothing to send: pass a URL, or use --request NAME")
		}
		req = model.RequestData{Method: "GET"}
		switch len(rest) {
		case 1:
			req.URL = rest[0]
		default:
			req.Method = strings.ToUpper(rest[0])
			req.URL = rest[1]
		}
		if body != "" {
			req.Body.Mode = model.BodyRaw
			req.Body.Raw = body
			req.Body.Lang = guessLang(body)
		} else {
			req.Body.Mode = model.BodyNone
		}
		req.Auth.Type = model.AuthNone
	}
	if body != "" && request != "" {
		req.Body.Mode = model.BodyRaw
		req.Body.Raw = body
		req.Body.Lang = guessLang(body)
	}
	for _, h := range headers {
		name, value, ok := strings.Cut(h, ":")
		if !ok {
			return fmt.Errorf("header %q must look like NAME: VALUE", h)
		}
		req.Headers = append(req.Headers, model.KV{Key: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
	}
	if insecure {
		req.Settings.SkipTLSVerify = true
	}
	if timeout > 0 {
		req.Settings.TimeoutMs = timeout * 1000
	}
	ctxTimeout := 60 * time.Second
	if req.Settings.TimeoutMs > 0 {
		ctxTimeout = time.Duration(req.Settings.TimeoutMs) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout+10*time.Second)
	defer cancel()
	resp := s.svc.SendWith(ctx, s.ws.ID, req, envID, collectionID, reqID, overrides)

	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "request failed: %s\n", resp.Error)
		if request != "" && strings.Contains(resp.Error, "authorization required") {
			fmt.Fprintf(os.Stderr, "hint: run %s authorize --request %q\n", brand.Slug, request)
		}
		return exitCode(1)
	}

	payload := []byte(resp.Body)
	if resp.Binary && resp.BodyBase64 != "" {
		if raw, err := base64.StdEncoding.DecodeString(resp.BodyBase64); err == nil {
			payload = raw
		}
	}
	if pretty && !resp.Binary && looksJSON(payload) {
		var buf bytes.Buffer
		if err := json.Indent(&buf, payload, "", "  "); err == nil {
			payload = buf.Bytes()
		}
	}
	if output != "" {
		if err := os.WriteFile(output, payload, 0o600); err != nil {
			return err
		}
	} else {
		os.Stdout.Write(payload)
		if len(payload) > 0 && payload[len(payload)-1] != '\n' {
			fmt.Println()
		}
	}

	sentURL := resp.URL
	if sentURL == "" {
		sentURL = req.URL
	}
	fmt.Fprintf(os.Stderr, "→ %s %s\n← %d %s · %d ms · %s\n",
		strings.ToUpper(req.Method), sentURL,
		resp.Status, resp.StatusText, resp.TimeMs, humanBytes(resp.Size))

	if failFast && resp.Status >= 400 {
		return exitCode(2)
	}
	if failTest {
		for _, test := range resp.Tests {
			if !test.Passed {
				return exitCode(3)
			}
		}
	}
	return nil
}

func cmdAuthorize(args []string) error {
	fs := flag.NewFlagSet("authorize", flag.ContinueOnError)
	var c commonFlags
	var request, envName string
	c.register(fs)
	fs.StringVar(&request, "request", "", "saved request name or id")
	fs.StringVar(&envName, "env", "", "environment name or id (default: active environment)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if request == "" {
		return fmt.Errorf("usage: %s authorize --request NAME [--env NAME]", brand.Slug)
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()

	e, err := s.findEntity(request, model.KindRequest)
	if err != nil {
		return err
	}
	req, err := service.RequestOf(e)
	if err != nil {
		return err
	}
	envID := s.envID(envName)
	if envName != "" && envID == "" {
		return fmt.Errorf("environment %q not found", envName)
	}
	if req.Auth.Type != model.AuthOAuth2 || req.Auth.OAuth2 == nil {
		return fmt.Errorf("request %q does not use OAuth 2.0 authentication", e.Name)
	}
	if req.Auth.OAuth2.GrantType != "" && req.Auth.OAuth2.GrantType != model.GrantAuthorizationCode {
		return fmt.Errorf("request %q uses the %s grant; authorization only applies to authorization code", e.Name, req.Auth.OAuth2.GrantType)
	}

	fmt.Fprintf(os.Stderr, "opening your browser to authorize %q…\n", e.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	expiry, err := s.svc.AuthorizeOAuth(ctx, s.ws.ID, req, envID, s.svc.CollectionIDFor(s.ws.ID, e.ID))
	if err != nil {
		return err
	}
	if expiry.IsZero() {
		fmt.Println("authorized; the provider did not report an expiry")
		return nil
	}
	fmt.Printf("authorized; token expires at %s\n", expiry.Local().Format(time.RFC3339))
	return nil
}

func parseVars(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, item := range raw {
		k, v, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("variable %q must look like name=value", item)
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, nil
}

func guessLang(body string) string {
	t := strings.TrimSpace(body)
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return "json"
	}
	return "text"
}

func looksJSON(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}

/* ------------------------------------------------------------------ */
/* Collection runner, backups, sync and devices                       */
/* ------------------------------------------------------------------ */

func cmdRunCollection(args []string) error {
	fs := flag.NewFlagSet("run-collection", flag.ContinueOnError)
	var c commonFlags
	var (
		envName  string
		failTest bool
		asJSON   bool
		vars     kvFlag
	)
	c.register(fs)
	fs.StringVar(&envName, "env", "", "environment name or id (default: active environment)")
	fs.BoolVar(&failTest, "fail-tests", false, "exit with status 3 when any request or test fails")
	fs.BoolVar(&asJSON, "json", false, "print the run summary as JSON")
	fs.Var(&vars, "var", "variable override, name=value (repeatable)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: %s run-collection <collection-or-folder> [--env NAME]", brand.Slug)
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	root, err := s.findEntity(fs.Arg(0), model.KindCollection, model.KindFolder)
	if err != nil {
		return err
	}
	overrides, err := parseVars(vars)
	if err != nil {
		return err
	}
	envID := s.envID(envName)
	if envName != "" && envID == "" {
		return fmt.Errorf("environment %q not found", envName)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	sum, err := s.svc.RunCollection(ctx, s.ws.ID, root.ID, envID, overrides)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(sum); err != nil {
			return err
		}
	} else {
		for _, item := range sum.Items {
			line := fmt.Sprintf("%3d  %-6s %s  %s", item.Status, strings.ToUpper(item.Method), item.Name, item.URL)
			if item.Error != "" {
				line += "  — " + item.Error
			} else if item.TestFail {
				line += "  — tests failed"
			} else if item.HTTPFail {
				line += "  — request failed"
			}
			fmt.Println(line)
		}
		fmt.Printf("\n%d requests · %d passed · %d failed · %d errors\n", sum.Total, sum.Passed, sum.Failed, sum.Errors)
	}
	if failTest && (sum.Failed > 0 || sum.Errors > 0) {
		return exitCode(3)
	}
	return nil
}

func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	var c commonFlags
	var out, passphrase string
	c.register(fs)
	fs.StringVar(&out, "out", "", "output file (default: stdout)")
	fs.StringVar(&out, "o", "", "output file (default: stdout)")
	fs.StringVar(&passphrase, "passphrase", "", "encrypt the backup with this passphrase (or YOURPM_BACKUP_PASSPHRASE)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if passphrase == "" {
		passphrase = os.Getenv("YOURPM_BACKUP_PASSPHRASE")
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	raw, err := s.svc.ExportWorkspaceEncrypted(s.ws.ID, passphrase)
	if err != nil {
		return err
	}
	if out == "" {
		os.Stdout.Write(raw)
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			fmt.Println()
		}
		if passphrase == "" {
			fmt.Fprintln(os.Stderr, "warning: plaintext backup; set --passphrase to encrypt it")
		}
		return nil
	}
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		return err
	}
	if passphrase == "" {
		fmt.Fprintf(os.Stderr, "workspace backup is plaintext; keep %s safe\n", out)
	} else {
		fmt.Fprintf(os.Stderr, "encrypted workspace backup written to %s\n", out)
	}
	return nil
}

func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	var c commonFlags
	var name, passphrase string
	c.register(fs)
	fs.StringVar(&name, "name", "", "name for the restored workspace")
	fs.StringVar(&passphrase, "passphrase", "", "passphrase for an encrypted backup (or YOURPM_BACKUP_PASSPHRASE)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: %s restore <backup.json> [--name NAME] [--passphrase PHRASE]", brand.Slug)
	}
	if passphrase == "" {
		passphrase = os.Getenv("YOURPM_BACKUP_PASSPHRASE")
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	svc, err := service.Open(c.dataDir)
	if err != nil {
		return err
	}
	defer svc.Close()
	if enabled, unlocked := svc.VaultStatus(); enabled && !unlocked {
		pass := os.Getenv("YOURPM_PASSPHRASE")
		if pass == "" {
			return errors.New("the vault is locked; set YOURPM_PASSPHRASE to unlock it")
		}
		if err := svc.VaultUnlock(pass); err != nil {
			return fmt.Errorf("could not unlock the vault: %w", err)
		}
	}
	ws, err := svc.ImportWorkspaceEncrypted(raw, name, passphrase)
	if errors.Is(err, service.ErrEncryptedBackup) {
		return errors.New("backup is encrypted; pass --passphrase or set YOURPM_BACKUP_PASSPHRASE")
	}
	if err != nil {
		return err
	}
	fmt.Printf("restored %q (%s)\n", ws.Name, ws.ID)
	return nil
}

func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	if !s.ws.Linked() {
		return errors.New("this workspace is not linked to a sync server")
	}
	client := syncl.New(s.svc.Store)
	if !client.LoggedIn() {
		return errors.New("not signed in; sign in from the desktop app first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	wsCopy := *s.ws
	res, err := client.Run(ctx, &wsCopy)
	if err != nil {
		return err
	}
	fmt.Printf("pushed %d · pulled %d · conflicts %d\n", res.Pushed, res.Pulled, res.Conflicts)
	for _, problem := range res.Errors {
		fmt.Fprintln(os.Stderr, "sync:", problem)
	}
	return nil
}

func cmdDevices(args []string) error {
	fs := flag.NewFlagSet("devices", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	client := syncl.New(s.svc.Store)
	if !client.LoggedIn() {
		return errors.New("not signed in; sign in from the desktop app first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := client.Devices(ctx)
	if err != nil {
		return err
	}
	current := client.Account().DeviceID
	for _, d := range list {
		mark := " "
		if d.ID == current {
			mark = "*"
		}
		state := ""
		if d.Revoked {
			state = "  revoked"
		}
		seen := "never"
		if d.LastSeen > 0 {
			seen = time.UnixMilli(d.LastSeen).Format("2006-01-02 15:04")
		}
		fmt.Printf("%s %-28s %s  last seen %s%s\n", mark, d.Name, d.ID, seen, state)
	}
	return nil
}

func cmdRevokeDevice(args []string) error {
	fs := flag.NewFlagSet("revoke-device", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: %s revoke-device <device-id>", brand.Slug)
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	client := syncl.New(s.svc.Store)
	if !client.LoggedIn() {
		return errors.New("not signed in; sign in from the desktop app first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := client.RevokeDevice(ctx, fs.Arg(0)); err != nil {
		return err
	}
	fmt.Println("device revoked")
	return nil
}

/* ------------------------------------------------------------------ */
/* Import / export                                                    */
/* ------------------------------------------------------------------ */

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: %s import <postman-collection.json>", brand.Slug)
	}
	path := fs.Arg(0)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	ent, err := s.svc.ImportPostman(s.ws.ID, raw)
	if err != nil {
		return err
	}
	fmt.Printf("imported %q into %s\n", ent.Name, s.ws.Name)
	return nil
}

func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	var c commonFlags
	var out string
	c.register(fs)
	fs.StringVar(&out, "out", "", "output file (default: stdout)")
	fs.StringVar(&out, "o", "", "output file (default: stdout)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: %s export <collection-or-environment> [--out file]", brand.Slug)
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	e, err := s.findEntity(fs.Arg(0), model.KindCollection, model.KindEnvironment)
	if err != nil {
		return err
	}
	var raw []byte
	switch e.Kind {
	case model.KindCollection:
		raw, err = s.svc.ExportCollection(e)
	case model.KindEnvironment:
		raw, err = s.svc.ExportEnvironment(e)
	}
	if err != nil {
		return err
	}
	if out == "" {
		os.Stdout.Write(raw)
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			fmt.Println()
		}
		return nil
	}
	return os.WriteFile(out, raw, 0o600)
}

/* ------------------------------------------------------------------ */
/* History                                                            */
/* ------------------------------------------------------------------ */

func cmdHistory(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	var c commonFlags
	limit := 20
	c.register(fs)
	fs.IntVar(&limit, "limit", 20, "number of entries")
	fs.IntVar(&limit, "n", 20, "number of entries (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.close()
	list, err := s.svc.History(s.ws.ID, limit)
	if err != nil {
		return err
	}
	for _, h := range list {
		when := time.UnixMilli(h.At).Format("2006-01-02 15:04:05")
		status := fmt.Sprintf("%d", h.Status)
		if h.Status == 0 {
			status = "ERR"
		}
		fmt.Printf("%s  %-7s %-4s %s\n", when, strings.ToUpper(h.Method), status, h.URL)
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

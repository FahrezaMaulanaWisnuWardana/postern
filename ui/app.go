package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/explorer"

	"yourpm/core/brand"
	"yourpm/core/model"
	"yourpm/core/service"
	syncl "yourpm/core/sync"
	"yourpm/core/vars"
)

const (
	sidebarW = unit.Dp(278)
	topbarH  = unit.Dp(54)
	statusH  = unit.Dp(26)
	tabbarH  = unit.Dp(40)
)

// Tab is one open editor tab.
type Tab struct {
	ID   string
	Kind string

	Req model.RequestData
	Env model.EnvironmentData
	Col model.CollectionData

	Resp    *model.Response
	Sending bool
	Dirty   bool
	saved   string

	reqTab  int
	respTab int

	// request editors
	url           widget.Editor
	body          widget.Editor
	pre           widget.Editor
	tests         widget.Editor
	authUser      widget.Editor
	authPass      widget.Editor
	authTok       widget.Editor
	authKey       widget.Editor
	authVal       widget.Editor
	oauthURL      widget.Editor
	oauthID       widget.Editor
	oauthSec      widget.Editor
	oauthSc       widget.Editor
	oauthAuth     widget.Editor
	oauthRedirect widget.Editor
	oauthUser     widget.Editor
	oauthPass     widget.Editor

	// authorization-code state (not persisted)
	oauthBusy bool
	oauthNote string
	oauthErr  bool

	params *kvEditor
	header *kvEditor
	form   *kvEditor
	urlenc *kvEditor

	// environment / collection
	envVars *kvEditor
	colVars *kvEditor
	colPre  widget.Editor
	colTest widget.Editor

	// binary body
	binaryPath widget.Editor

	// transport settings
	timeout  widget.Editor
	maxRedir widget.Editor
	proxy    widget.Editor

	// response
	respBody     widget.Editor
	requestJSON  jsonView
	responseJSON jsonView
	respScroll   layout.List
	metaScroll   layout.List

	bodyMode int
	bodyLang int
	authType int
	init     bool
}

// App is the whole native application state.
type App struct {
	win       *app.Window
	th        *material.Theme
	colorMode string
	svc       *service.Service
	syn       *syncl.Client
	ex        *explorer.Explorer
	ic        icons

	mu      sync.Mutex
	pending []func()

	ready       bool
	loading     bool
	bootErr     string
	locked      bool
	lockPass    widget.Editor
	lockErr     string
	lockFocused bool
	keysFocused bool

	workspaces []*model.Workspace
	wsID       string
	entities   []*model.Entity
	envs       []*model.Entity
	activeEnv  string
	vaultOn    bool

	selected string
	expanded map[string]bool
	tree     layout.List
	search   widget.Editor
	filter   string

	tabs    []*Tab
	active  int
	tabList layout.List

	dialog  *dialog
	menu    *menu
	toast   string
	toastAt time.Time

	historyOpen bool
	history     []*model.HistoryEntry
	histList    layout.List
	histQuery   widget.Editor

	split     float32
	splitDrag widget.Clickable
	splitBase int
	dragging  bool
	dragStart image.Point

	syncing  bool
	syncNote string
	syncWhen time.Time
	dirtyAt  time.Time

	// window and inactivity tracking
	winW, winH int
	lastActive time.Time
	lastAuto   time.Time
	activity   struct{}

	// collection run and trash state
	runBusy   bool
	run       *service.RunSummary
	runList   layout.List
	trash     []*model.Entity
	trashList layout.List

	settings settingsState
	clicks   map[string]*widget.Clickable
	editors  map[string]*widget.Editor
	keys     struct{}
}

type settingsState struct {
	scroll      layout.List
	team        *model.WorkspaceTeam
	invitations []model.WorkspaceInvitation
	teamBusy    bool
	teamRemote  string
	teamAccount string
	teamNote    string
	inviteEmail widget.Editor
	inviteRole  string
	inviteCode  widget.Editor
	joinCode    widget.Editor
	joinPass    widget.Editor
	tab         int
	server      widget.Editor
	email       widget.Editor
	password    widget.Editor
	passphrase  widget.Editor
	remote      []model.RemoteWorkspace
	devices     []syncl.Device
	devicesBusy bool
	busy        bool
	note        string
}

// Run starts the native application. It blocks until the window closes.
func Run() error {
	dataDir, err := service.DefaultDataDir()
	if err != nil {
		return err
	}
	svc, err := service.Open(dataDir)
	if err != nil {
		return err
	}
	defer svc.Close()

	a := &App{
		svc:      svc,
		syn:      syncl.New(svc.Store),
		expanded: map[string]bool{},
		split:    0.52,
		clicks:   map[string]*widget.Clickable{},
		editors:  map[string]*widget.Editor{},
		ic:       loadIcons(),
	}
	a.tabList.Axis = layout.Horizontal
	a.colorMode = svc.Store.Setting("ui.theme")
	applyColorMode(a.colorMode)
	a.tree.Axis = layout.Vertical
	a.histList.Axis = layout.Vertical
	a.lastActive = time.Now()
	a.winW = atoiSafe(a.svc.Store.Setting("ui.winW"))
	a.winH = atoiSafe(a.svc.Store.Setting("ui.winH"))
	server := a.svc.Store.Setting("sync.server")
	if server == "" {
		server = brand.DefaultServer
	}
	a.settings.server.SetText(server)
	a.settings.email.SetText(a.svc.Store.Setting("sync.email"))

	go func() {
		defer func() {
			_ = svc.Close()
			os.Exit(0)
		}()
		w := new(app.Window)
		sizeW, sizeH := unit.Dp(1400), unit.Dp(900)
		if a.winW >= 980 {
			sizeW = unit.Dp(a.winW)
		}
		if a.winH >= 620 {
			sizeH = unit.Dp(a.winH)
		}
		w.Option(
			app.Title(brand.Name),
			app.Size(sizeW, sizeH),
			app.MinSize(unit.Dp(980), unit.Dp(620)),
		)
		a.win = w
		a.th = newTheme()
		a.ex = explorer.NewExplorer(w)
		a.load()
		if err := a.loop(w); err != nil {
			fmt.Fprintln(os.Stderr, "yourpm:", err)
		}
	}()
	app.Main()
	return nil
}

func (a *App) loop(w *app.Window) error {
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			a.shutdown()
			return e.Err
		case app.FrameEvent:
			a.ex.ListenEvents(e)
			if e.Metric.PxPerDp > 0 && e.Size.X > 0 && e.Size.Y > 0 {
				a.winW = int(float32(e.Size.X) / e.Metric.PxPerDp)
				a.winH = int(float32(e.Size.Y) / e.Metric.PxPerDp)
			}
			gtx := app.NewContext(&ops, e)
			a.handleKeys(gtx)
			a.Layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

// Post runs fn on the UI goroutine.
func (a *App) Post(fn func()) {
	a.mu.Lock()
	a.pending = append(a.pending, fn)
	a.mu.Unlock()
	if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *App) drain() {
	a.mu.Lock()
	fns := a.pending
	a.pending = nil
	a.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

/* ------------------------------------------------------------------ */
/* Data                                                               */
/* ------------------------------------------------------------------ */

func (a *App) load() {
	a.loading = true
	go func() {
		b, err := a.svc.Load(a.wsID)
		a.Post(func() {
			a.loading = false
			a.ready = true
			if err != nil {
				a.bootErr = err.Error()
				return
			}
			a.apply(b)
		})
	}()
}

func (a *App) apply(b *service.Bootstrap) {
	a.workspaces = b.Workspaces
	a.wsID = b.Active
	if a.wsID != "" {
		if err := a.svc.Store.SetSetting("ui.workspace", a.wsID); err != nil {
			_ = err
		}
	}
	a.vaultOn = b.VaultEnabled
	a.locked = b.VaultEnabled && !b.VaultUnlocked
	if a.locked {
		return
	}
	a.entities = b.Entities
	a.rebuildEnvList()
	envKey := "ui.env." + a.wsID
	if v := a.svc.Store.Setting(envKey); v != "" {
		a.activeEnv = v
	} else if len(a.envs) > 0 {
		a.activeEnv = a.envs[0].ID
	}
	a.ensureExpanded()
}

func (a *App) rebuildEnvList() {
	a.envs = nil
	for _, e := range a.entities {
		if e.Kind == model.KindEnvironment {
			a.envs = append(a.envs, e)
		}
	}
	sort.SliceStable(a.envs, func(i, j int) bool { return a.envs[i].Name < a.envs[j].Name })
}

func (a *App) ensureExpanded() {
	for _, e := range a.entities {
		if e.Kind == model.KindCollection {
			if _, ok := a.expanded[e.ID]; !ok {
				a.expanded[e.ID] = true
			}
		}
	}
}

func (a *App) reloadEntities() {
	ents, err := a.svc.Store.ListEntities(a.wsID, false)
	if err != nil {
		return
	}
	a.entities = ents
	a.rebuildEnvList()
}

func (a *App) entityByID(id string) *model.Entity {
	for _, e := range a.entities {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (a *App) workspace() *model.Workspace {
	for _, w := range a.workspaces {
		if w.ID == a.wsID {
			return w
		}
	}
	return nil
}

/* ------------------------------------------------------------------ */
/* Tabs                                                               */
/* ------------------------------------------------------------------ */

func (a *App) openEntity(e *model.Entity) {
	a.selected = e.ID
	for i, t := range a.tabs {
		if t.ID == e.ID {
			a.active = i
			return
		}
	}
	t := &Tab{ID: e.ID, Kind: e.Kind, bodyMode: 0, bodyLang: 0, authType: 0}
	a.initTab(t, e)
	a.tabs = append(a.tabs, t)
	a.active = len(a.tabs) - 1
}

func (a *App) initTab(t *Tab, e *model.Entity) {
	if t.init {
		return
	}
	t.init = true
	t.respScroll.Axis = layout.Vertical
	t.metaScroll.Axis = layout.Vertical
	t.url.SingleLine = true
	t.authUser.SingleLine = true
	t.authPass.SingleLine = true
	t.authTok.SingleLine = true
	t.authKey.SingleLine = true
	t.authVal.SingleLine = true
	t.oauthURL.SingleLine = true
	t.oauthID.SingleLine = true
	t.oauthSec.SingleLine = true
	t.oauthSc.SingleLine = true
	t.oauthAuth.SingleLine = true
	t.oauthRedirect.SingleLine = true
	t.oauthUser.SingleLine = true
	t.oauthPass.SingleLine = true
	t.binaryPath.SingleLine = true
	t.timeout.SingleLine = true
	t.maxRedir.SingleLine = true
	t.proxy.SingleLine = true
	t.body.SingleLine = false
	t.pre.SingleLine = false
	t.tests.SingleLine = false
	t.colPre.SingleLine = false
	t.colTest.SingleLine = false
	t.respBody.SingleLine = false
	t.respBody.ReadOnly = true

	switch e.Kind {
	case model.KindRequest:
		req, _ := service.RequestOf(e)
		t.Req = req
		t.url.SetText(req.URL)
		t.body.SetText(req.Body.Raw)
		t.binaryPath.SetText(req.Body.FilePath)
		if req.Settings.TimeoutMs > 0 {
			t.timeout.SetText(itoa(req.Settings.TimeoutMs))
		}
		if req.Settings.MaxRedirects > 0 {
			t.maxRedir.SetText(itoa(req.Settings.MaxRedirects))
		}
		t.proxy.SetText(req.Settings.Proxy)
		t.pre.SetText(req.PreRequestScript)
		t.tests.SetText(req.TestScript)
		t.authTok.SetText(req.Auth.Token)
		t.authUser.SetText(req.Auth.Username)
		t.authPass.SetText(req.Auth.Password)
		t.authKey.SetText(req.Auth.Key)
		t.authVal.SetText(req.Auth.Value)
		if req.Auth.OAuth2 != nil {
			t.oauthURL.SetText(req.Auth.OAuth2.AccessTokenURL)
			t.oauthID.SetText(req.Auth.OAuth2.ClientID)
			t.oauthSec.SetText(req.Auth.OAuth2.ClientSecret)
			t.oauthSc.SetText(req.Auth.OAuth2.Scope)
			t.oauthAuth.SetText(req.Auth.OAuth2.AuthorizationURL)
			t.oauthRedirect.SetText(req.Auth.OAuth2.RedirectURL)
			t.oauthUser.SetText(req.Auth.OAuth2.Username)
			t.oauthPass.SetText(req.Auth.OAuth2.Password)
		}
		t.params = newKV(req.Params)
		t.header = newKV(req.Headers)
		t.form = newFormEditor(req.Body.FormData)
		t.urlenc = newKV(req.Body.URLEncoded)
		t.bodyMode = bodyModeIndex(req.Body.Mode)
		t.bodyLang = langIndex(req.Body.Lang)
		t.authType = authIndex(req.Auth.Type)
	case model.KindEnvironment:
		env, _ := service.EnvironmentOf(e)
		t.Env = env
		t.envVars = newVarEditor(env.Variables)
	case model.KindCollection:
		col, _ := service.CollectionOf(e)
		t.Col = col
		t.colVars = newVarEditor(col.Variables)
		t.colPre.SetText(col.PreRequestScript)
		t.colTest.SetText(col.TestScript)
	}
	t.saved = a.tabJSON(t)
}

// refreshDirty updates the unsaved markers for open tabs. It is throttled
// because serialising every document on every frame is wasteful.
func (a *App) refreshDirty() {
	if time.Since(a.dirtyAt) < 400*time.Millisecond {
		return
	}
	a.dirtyAt = time.Now()
	for _, t := range a.tabs {
		a.pullEditors(t)
	}
}

// shutdown persists the window size and unsaved editors before the process
// exits. Gio has no close-request veto, so saving is the safe choice.
func (a *App) shutdown() {
	a.saveDirtyTabs()
	if a.winW > 0 {
		_ = a.svc.Store.SetSetting("ui.winW", itoa(a.winW))
		_ = a.svc.Store.SetSetting("ui.winH", itoa(a.winH))
	}
}

func (a *App) saveDirtyTabs() {
	for _, t := range a.tabs {
		a.pullEditors(t)
		if t.Dirty {
			a.saveTab(t)
		}
	}
}

func (a *App) tabIndex(t *Tab) int {
	for i, other := range a.tabs {
		if other == t {
			return i
		}
	}
	return -1
}

// closeTab asks before discarding unsaved edits, then removes the tab.
func (a *App) closeTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	t := a.tabs[i]
	a.pullEditors(t)
	if t.Dirty {
		a.confirmCloseTab(t)
		return
	}
	a.removeTab(i)
}

func (a *App) confirmCloseTab(t *Tab) {
	name := "Untitled"
	if e := a.entityByID(t.ID); e != nil {
		name = e.Name
	}
	d := &dialog{
		kind:     "confirm",
		title:    "Close “" + name + "”?",
		message:  "This tab has unsaved changes. Save them before closing, or discard them.",
		okLabel:  "Save",
		altLabel: "Discard",
	}
	d.onOK = func(a *App, d *dialog) {
		a.saveTab(t)
		if i := a.tabIndex(t); i >= 0 {
			a.removeTab(i)
		}
		a.dialog = nil
	}
	d.onAlt = func(a *App, d *dialog) {
		if i := a.tabIndex(t); i >= 0 {
			a.removeTab(i)
		}
		a.dialog = nil
	}
	d.onCancel = func(a *App) {}
	a.dialog = d
	a.win.Invalidate()
}

func (a *App) removeTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if a.active >= len(a.tabs) {
		a.active = len(a.tabs) - 1
	}
	if a.active < 0 {
		a.active = 0
	}
}

func (a *App) activeTab() *Tab {
	if a.active < 0 || a.active >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.active]
}

func (a *App) tabJSON(t *Tab) string {
	switch t.Kind {
	case model.KindRequest:
		b, _ := json.Marshal(t.Req)
		return string(b)
	case model.KindEnvironment:
		b, _ := json.Marshal(t.Env)
		return string(b)
	case model.KindCollection:
		b, _ := json.Marshal(t.Col)
		return string(b)
	}
	return ""
}

// pullEditors copies widget state back into the tab model.
func (a *App) pullEditors(t *Tab) {
	switch t.Kind {
	case model.KindRequest:
		t.Req.URL = t.url.Text()
		t.Req.Body.Raw = t.body.Text()
		t.Req.Body.FilePath = t.binaryPath.Text()
		t.Req.Settings.TimeoutMs = atoiSafe(t.timeout.Text())
		t.Req.Settings.MaxRedirects = atoiSafe(t.maxRedir.Text())
		t.Req.Settings.Proxy = strings.TrimSpace(t.proxy.Text())
		t.Req.PreRequestScript = t.pre.Text()
		t.Req.TestScript = t.tests.Text()
		t.Req.Auth.Token = t.authTok.Text()
		t.Req.Auth.Username = t.authUser.Text()
		t.Req.Auth.Password = t.authPass.Text()
		t.Req.Auth.Key = t.authKey.Text()
		t.Req.Auth.Value = t.authVal.Text()
		if t.Req.Auth.OAuth2 != nil {
			t.Req.Auth.OAuth2.AccessTokenURL = t.oauthURL.Text()
			t.Req.Auth.OAuth2.ClientID = t.oauthID.Text()
			t.Req.Auth.OAuth2.ClientSecret = t.oauthSec.Text()
			t.Req.Auth.OAuth2.Scope = t.oauthSc.Text()
			t.Req.Auth.OAuth2.AuthorizationURL = t.oauthAuth.Text()
			t.Req.Auth.OAuth2.RedirectURL = t.oauthRedirect.Text()
			t.Req.Auth.OAuth2.Username = t.oauthUser.Text()
			t.Req.Auth.OAuth2.Password = t.oauthPass.Text()
		}
		if t.params != nil {
			t.Req.Params = t.params.Model()
		}
		if t.header != nil {
			t.Req.Headers = t.header.Model()
		}
		if t.form != nil {
			t.Req.Body.FormData = t.form.Model()
		}
		if t.urlenc != nil {
			t.Req.Body.URLEncoded = t.urlenc.Model()
		}
	case model.KindEnvironment:
		if t.envVars != nil {
			t.Env.Variables = t.envVars.Model()
		}
	case model.KindCollection:
		if t.colVars != nil {
			t.Col.Variables = t.colVars.Model()
		}
		t.Col.PreRequestScript = t.colPre.Text()
		t.Col.TestScript = t.colTest.Text()
	}
	t.Dirty = a.tabJSON(t) != t.saved
}

func (a *App) saveTab(t *Tab) {
	a.pullEditors(t)
	e := a.entityByID(t.ID)
	if e == nil {
		return
	}
	switch t.Kind {
	case model.KindRequest:
		_ = service.SetRequest(e, t.Req)
	case model.KindEnvironment:
		_ = service.SetEnvironment(e, t.Env)
	case model.KindCollection:
		_ = service.SetCollection(e, t.Col)
	}
	if err := a.svc.SaveEntity(e); err != nil {
		a.notify("Could not save: " + err.Error())
		return
	}
	t.saved = a.tabJSON(t)
	t.Dirty = false
	a.reloadEntities()
}

func (a *App) send(t *Tab) {
	if t.Sending {
		return
	}
	a.pullEditors(t)
	e := a.entityByID(t.ID)
	if e == nil {
		return
	}
	_ = service.SetRequest(e, t.Req)
	_ = a.svc.SaveEntity(e)
	t.Dirty = false
	t.saved = a.tabJSON(t)
	t.Sending = true

	ws := a.wsID
	env := a.activeEnv
	col := a.svc.CollectionIDFor(ws, t.ID)
	req := cloneRequestData(t.Req)
	reqID := t.ID

	go func() {
		resp := a.svc.Send(context.Background(), ws, req, env, col, reqID)
		a.Post(func() {
			t.Sending = false
			t.Resp = resp
			t.respTab = 0
			t.respBody.SetText(displayBody(resp))
			t.respScroll.Position.First = 0
			t.respScroll.Position.Offset = 0
			a.reloadEntities()
			if resp.Error != "" {
				a.notify("Request failed: " + resp.Error)
			}
		})
	}()
}

func (a *App) authorizeOAuth(t *Tab) {
	if t.oauthBusy || t.Req.Auth.OAuth2 == nil {
		return
	}
	a.pullEditors(t)
	e := a.entityByID(t.ID)
	if e == nil {
		return
	}
	_ = service.SetRequest(e, t.Req)
	_ = a.svc.SaveEntity(e)
	t.Dirty = false
	t.saved = a.tabJSON(t)
	t.oauthBusy = true
	t.oauthErr = false
	t.oauthNote = "Waiting for the browser to finish authorization…"
	a.win.Invalidate()

	ws := a.wsID
	env := a.activeEnv
	col := a.svc.CollectionIDFor(ws, t.ID)
	req := t.Req
	go func() {
		expiry, err := a.svc.AuthorizeOAuth(context.Background(), ws, req, env, col)
		a.Post(func() {
			t.oauthBusy = false
			if err != nil {
				t.oauthErr = true
				t.oauthNote = "Authorization failed: " + err.Error()
				a.notify("Authorization failed: " + err.Error())
				return
			}
			t.oauthErr = false
			if expiry.IsZero() {
				t.oauthNote = "Authorized. The provider did not report an expiry; refresh runs when the token is refused."
			} else {
				t.oauthNote = "Authorized. Token expires at " + expiry.Local().Format("15:04:05") + "."
			}
			a.notify("OAuth 2.0 authorization complete.")
		})
	}()
}

func displayBody(resp *model.Response) string {
	if resp == nil {
		return ""
	}
	if resp.Binary {
		return "[binary response, " + humanBytes(resp.Size) + "]"
	}
	body := resp.Body
	if len(body) > 512<<10 {
		return body[:512<<10] + "\n\n… response truncated for display (use cURL or the CLI for the full body)"
	}
	if strings.Contains(strings.ToLower(headerValue(resp.Headers, "content-type")), "json") || looksJSON(body) {
		var buf bytes.Buffer
		if err := json.Indent(&buf, []byte(body), "", "  "); err == nil {
			return buf.String()
		}
	}
	return body
}

func looksJSON(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
}

func headerValue(kvs []model.KV, key string) string {
	for _, h := range kvs {
		if strings.EqualFold(h.Key, key) {
			return h.Value
		}
	}
	return ""
}

/* ------------------------------------------------------------------ */
/* Helpers                                                            */
/* ------------------------------------------------------------------ */

func (a *App) click(key string) *widget.Clickable {
	if c, ok := a.clicks[key]; ok {
		return c
	}
	c := new(widget.Clickable)
	a.clicks[key] = c
	return c
}

func (a *App) editor(key string) *widget.Editor {
	if e, ok := a.editors[key]; ok {
		return e
	}
	e := new(widget.Editor)
	a.editors[key] = e
	return e
}

func (a *App) notify(msg string) {
	a.toast = msg
	a.toastAt = time.Now()
	if a.win != nil {
		a.win.Invalidate()
	}
	at := a.toastAt
	time.AfterFunc(5*time.Second, func() {
		a.Post(func() {
			if a.toastAt.Equal(at) {
				a.toast = ""
			}
		})
	})
}

func (a *App) setWorkspace(id string) {
	a.saveDirtyTabs()
	a.wsID = id
	a.tabs = nil
	a.active = 0
	a.selected = ""
	a.load()
}

func (a *App) setEnvironment(id string) {
	a.activeEnv = id
	_ = a.svc.Store.SetSetting("ui.env."+a.wsID, id)
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
}

func humanAgo(ms int64) string {
	if ms == 0 {
		return "never"
	}
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

var _ = vars.Interpolate

// ensure graphics imports used across UI files
var _ = paint.Fill
var _ = clip.Rect{}

func bodyModeIndex(mode string) int {
	for i, m := range bodyModes {
		if m.mode == mode {
			return i
		}
	}
	return 0
}

func langIndex(lang string) int {
	for i, l := range bodyLangs {
		if l == lang {
			return i
		}
	}
	return 0
}

func authIndex(kind string) int {
	for i, a := range authTypes {
		if a.kind == kind {
			return i
		}
	}
	if kind == "" {
		return 0
	}
	return 1
}

func cloneRequestData(r model.RequestData) model.RequestData {
	r.Params = append([]model.KV(nil), r.Params...)
	r.Headers = append([]model.KV(nil), r.Headers...)
	r.Body.FormData = append([]model.KV(nil), r.Body.FormData...)
	r.Body.URLEncoded = append([]model.KV(nil), r.Body.URLEncoded...)
	if r.Auth.OAuth2 != nil {
		cp := *r.Auth.OAuth2
		r.Auth.OAuth2 = &cp
	}
	return r
}

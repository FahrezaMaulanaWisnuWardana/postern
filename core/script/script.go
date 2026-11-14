// Package script runs Postman-style pre-request and test scripts in an
// embedded JavaScript runtime. Scripts can read/write variables, inspect the
// request and, for tests, the response.
package script

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"

	"yourpm/core/model"
)

// RequestInfo is the request view exposed to scripts as pm.request.
type RequestInfo struct {
	Method  string
	URL     string
	Headers []model.KV
}

// ResponseInfo is the response view exposed as pm.response.
type ResponseInfo struct {
	Code    int
	Status  string
	Body    string
	Headers map[string]string
	TimeMs  int64
}

// Context is the script execution environment.
type Context struct {
	Vars     map[string]string
	Request  RequestInfo
	Response *ResponseInfo
	EnvName  string
	Timeout  time.Duration
}

// Output is the result of a script run.
type Output struct {
	Console    []string
	EnvUpdates map[string]string
	EnvUnsets  []string
	Tests      []model.TestResult
	Error      string
}

const prelude = `
var __console = [];
var __tests = [];
var __fmt = function (v) {
  if (typeof v === 'string') return v;
  if (v === undefined) return 'undefined';
  if (v === null) return 'null';
  try { return JSON.stringify(v); } catch (e) { return String(v); }
};
console = {
  log: function () { __console.push(Array.prototype.map.call(arguments, __fmt).join(' ')); },
  info: function () { __console.push(Array.prototype.map.call(arguments, __fmt).join(' ')); },
  warn: function () { __console.push('warn: ' + Array.prototype.map.call(arguments, __fmt).join(' ')); },
  error: function () { __console.push('error: ' + Array.prototype.map.call(arguments, __fmt).join(' ')); }
};
function __expect(value) {
  var negate = false;
  var api = {};
  function check(cond, msg) {
    if (negate ? cond : !cond) {
      throw new Error('expected ' + (negate ? 'not ' : '') + msg);
    }
  }
  api.to = api; api.be = api; api.have = api; api.and = api; api.that = api; api.is = api;
  Object.defineProperty(api, 'not', { get: function () { negate = !negate; return api; } });
  var __deepEq = function (a, b) {
    if (a === b) return true;
    if (a === null || b === null || typeof a !== 'object' || typeof b !== 'object') return false;
    if (Array.isArray(a) !== Array.isArray(b)) return false;
    var ka = Object.keys(a), kb = Object.keys(b);
    if (ka.length !== kb.length) return false;
    for (var i = 0; i < ka.length; i++) {
      var k = ka[i];
      if (!Object.prototype.hasOwnProperty.call(b, k) || !__deepEq(a[k], b[k])) return false;
    }
    return true;
  };
  api.equal = function (x) { check(value === x, 'to equal ' + __fmt(x)); return api; };
  api.eql = function (x) { check(__deepEq(value, x), 'to deeply equal ' + __fmt(x)); return api; };
  api.include = function (x) {
    var ok = Array.isArray(value) ? value.indexOf(x) >= 0 : String(value).indexOf(x) >= 0;
    check(ok, 'to include ' + __fmt(x));
    return api;
  };
  api.match = function (re) { check(new RegExp(re).test(String(value)), 'to match ' + re); return api; };
  api.above = function (n) { check(value > n, 'to be above ' + n); return api; };
  api.below = function (n) { check(value < n, 'to be below ' + n); return api; };
  api.least = function (n) { check(value >= n, 'to be at least ' + n); return api; };
  api.most = function (n) { check(value <= n, 'to be at most ' + n); return api; };
  api.a = function (t) { check(typeof value === t, 'to be a ' + t); return api; };
  api.property = function (name, val) {
    check(value !== null && value !== undefined && name in Object(value), 'to have property ' + name);
    if (arguments.length > 1) check(value[name] === val, 'property ' + name + ' to equal ' + __fmt(val));
    return api;
  };
  ['true','false','null','undefined'].forEach(function (k) {
    Object.defineProperty(api, k, { get: function () {
      check(k === 'null' ? value === null : k === 'undefined' ? value === undefined : String(value) === k, 'to be ' + k);
      return api;
    }});
  });
  Object.defineProperty(api, 'ok', { get: function () { check(!!value, 'to be truthy'); return api; } });
  Object.defineProperty(api, 'empty', { get: function () { check(value && value.length === 0, 'to be empty'); return api; } });
  return api;
}
var __varScope = {
  get: function (k) { return __get(String(k)); },
  set: function (k, v) { __set(String(k), String(v)); },
  has: function (k) { return __has(String(k)); },
  unset: function (k) { __unset(String(k)); }
};
var pm = {
  variables: __varScope,
  environment: { get: __varScope.get, set: __varScope.set, has: __varScope.has, unset: __varScope.unset, name: __envName },
  collectionVariables: __varScope,
  globals: __varScope,
  iterationData: {},
  info: { eventName: __eventName },
  request: {
    method: __req.method,
    url: __req.url,
    headers: { get: function (k) { return __req.headers[String(k).toLowerCase()]; } }
  },
  response: __resp ? {
    code: __resp.code,
    status: __resp.status,
    responseTime: __resp.timeMs,
    text: function () { return __resp.body; },
    json: function () { return JSON.parse(__resp.body); },
    headers: { get: function (k) { return __resp.headers[String(k).toLowerCase()]; } }
  } : undefined,
  test: function (name, fn) {
    try { fn(); __tests.push({ name: String(name), passed: true }); }
    catch (e) { __tests.push({ name: String(name), passed: false, error: String(e && e.message ? e.message : e) }); }
  },
  expect: __expect
};
`

// Run executes src against ctx and returns the observable effects.
func Run(src string, ctx *Context) *Output {
	out := &Output{EnvUpdates: map[string]string{}}
	if strings.TrimSpace(src) == "" {
		return out
	}
	timeout := ctx.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	rt := goja.New()
	rt.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	hdrs := map[string]string{}
	for _, h := range ctx.Request.Headers {
		if h.Disabled {
			continue
		}
		hdrs[strings.ToLower(h.Key)] = h.Value
	}
	eventName := "prerequest"
	if ctx.Response != nil {
		eventName = "test"
	}
	envName := ctx.EnvName
	if envName == "" {
		envName = "environment"
	}

	set := func(name string, v any) {
		if err := rt.Set(name, v); err != nil {
			out.Error = err.Error()
		}
	}
	set("__envName", envName)
	set("__eventName", eventName)
	set("__req", map[string]any{"method": ctx.Request.Method, "url": ctx.Request.URL, "headers": hdrs})
	if ctx.Response != nil {
		respHeaders := map[string]string{}
		for k, v := range ctx.Response.Headers {
			respHeaders[strings.ToLower(k)] = v
		}
		set("__resp", map[string]any{
			"code":    ctx.Response.Code,
			"status":  ctx.Response.Status,
			"body":    ctx.Response.Body,
			"headers": respHeaders,
			"timeMs":  ctx.Response.TimeMs,
		})
	} else {
		set("__resp", nil)
	}

	must := func(err error) {
		if err != nil && out.Error == "" {
			out.Error = err.Error()
		}
	}
	must(rt.Set("__get", func(k string) goja.Value {
		if v, ok := ctx.Vars[k]; ok {
			return rt.ToValue(v)
		}
		return goja.Undefined()
	}))
	must(rt.Set("__has", func(k string) bool {
		_, ok := ctx.Vars[k]
		return ok
	}))
	must(rt.Set("__set", func(k, v string) {
		if ctx.Vars == nil {
			ctx.Vars = map[string]string{}
		}
		ctx.Vars[k] = v
		out.EnvUpdates[k] = v
	}))
	must(rt.Set("__unset", func(k string) {
		delete(ctx.Vars, k)
		delete(out.EnvUpdates, k)
		out.EnvUnsets = append(out.EnvUnsets, k)
	}))

	rt.RunString(prelude)

	var timer *time.Timer
	timer = time.AfterFunc(timeout, func() { rt.Interrupt("script timed out after " + timeout.String()) })
	defer timer.Stop()

	if _, err := rt.RunString(src); err != nil {
		if out.Error == "" {
			out.Error = cleanErr(err)
		}
	}

	out.Console = exportStrings(rt, "__console")
	for _, t := range exportTests(rt) {
		out.Tests = append(out.Tests, t)
	}
	return out
}

func cleanErr(err error) string {
	if ie, ok := err.(*goja.InterruptedError); ok {
		return fmt.Sprint(ie.Value())
	}
	msg := err.Error()
	if idx := strings.Index(msg, " at <eval>"); idx > 0 {
		msg = msg[:idx]
	}
	return msg
}

func exportStrings(rt *goja.Runtime, name string) []string {
	v := rt.Get(name)
	if v == nil {
		return nil
	}
	exported, ok := v.Export().([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(exported))
	for _, item := range exported {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

func exportTests(rt *goja.Runtime) []model.TestResult {
	v := rt.Get("__tests")
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v.Export())
	if err != nil {
		return nil
	}
	var list []struct {
		Name   string `json:"name"`
		Passed bool   `json:"passed"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	out := make([]model.TestResult, 0, len(list))
	for _, t := range list {
		out = append(out, model.TestResult{Name: t.Name, Passed: t.Passed, Error: t.Error})
	}
	return out
}

func exportMap(rt *goja.Runtime, name string) map[string]string {
	out := map[string]string{}
	v := rt.Get(name)
	if v == nil {
		return out
	}
	obj, ok := v.Export().(map[string]any)
	if !ok {
		return out
	}
	for k, val := range obj {
		out[k] = fmt.Sprint(val)
	}
	return out
}

// Command brandgen turns a small white-label description into linker flags.
//
//	brandgen -in brand/acme.yaml -format flags
//	brandgen -in brand/acme.yaml -format go
//
// The input is a deliberately tiny subset of YAML: "key: value" lines, where
// values may be quoted and "#" starts a comment. Keeping the parser here means
// white-label packaging does not pull a YAML dependency into the product.
package main

import (
	"bufio"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// field maps a brand-file key to the linker symbol that receives it.
var field = map[string]string{
	"name":    "yourpm/core/brand.Name",
	"slug":    "yourpm/core/brand.Slug",
	"version": "yourpm/core/brand.Version",
	"tagline": "yourpm/core/brand.Tagline",
	"server":  "yourpm/core/brand.DefaultServer",
	"accent":  "yourpm/core/brand.Accent",
	"website": "yourpm/core/brand.Website",
	"logo":    "yourpm/core/brand.Logo",
}

func main() {
	in := flag.String("in", "", "brand file to read")
	format := flag.String("format", "flags", "output format: flags, make or go")
	get := flag.String("get", "", "print a single value, e.g. -get slug")
	flag.Parse()
	if *in == "" {
		fmt.Fprintln(os.Stderr, "brandgen: -in is required")
		os.Exit(2)
	}
	values, err := parseFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "brandgen:", err)
		os.Exit(1)
	}
	if *get != "" {
		v, ok := values[strings.ToLower(*get)]
		if !ok {
			fmt.Fprintf(os.Stderr, "brandgen: %q is not set in %s\n", *get, *in)
			os.Exit(1)
		}
		fmt.Println(v)
		return
	}
	// Link the embedded logo as base64. -get returns the raw path so build
	// scripts can use it as a prerequisite.
	if path := values["logo"]; path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "brandgen: logo:", err)
			os.Exit(1)
		}
		values["logo"] = base64.StdEncoding.EncodeToString(raw)
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	switch *format {
	case "flags":
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("-X %s=%s", field[k], values[k]))
		}
		fmt.Println(strings.Join(parts, " "))
	case "make", "ldflags":
		for _, k := range keys {
			fmt.Println(ldflagToken(field[k], values[k]))
		}
	case "go":
		for _, k := range keys {
			fmt.Printf("-X %q\n", field[k]+"="+values[k])
		}
	default:
		fmt.Fprintln(os.Stderr, "brandgen: unknown -format", *format)
		os.Exit(2)
	}
}

func parseFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, ":")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key: value", path, line)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = cleanValue(value)
		if _, ok := field[key]; !ok {
			return nil, fmt.Errorf("%s:%d: unknown key %q (known: accent, logo, name, server, slug, tagline, version, website)", path, line, key)
		}
		if key == "logo" && value != "" {
			// Keep the raw path; it is encoded only for linker output so
			// -get logo can serve as a Make prerequisite.
			out[key] = value
			continue
		}
		out[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ldflagToken renders one `-X symbol=value` linker flag so it survives both the
// shell double-quoted context the Makefile puts it in and Go's own quoted
// splitting of the -ldflags value.
//
// Go splits -ldflags on spaces and treats single or double quotes as
// delimiters without any escaping, so the delimiter is chosen to avoid quotes
// in the value. The token is then escaped for the surrounding `"…"` shell
// string: backslash, double quote, dollar and backtick all need a backslash.
// A value containing both quote kinds is outside what -ldflags can express;
// its apostrophes become typographic ones rather than breaking the build.
func ldflagToken(symbol, value string) string {
	quote := byte('\'')
	if strings.Contains(value, "'") {
		if strings.Contains(value, `"`) {
			value = strings.ReplaceAll(value, "'", "’")
		} else {
			quote = '"'
		}
	}
	token := "-X " + string(quote) + symbol + "=" + value + string(quote)

	var b strings.Builder
	b.Grow(len(token) + 8)
	for i := 0; i < len(token); i++ {
		switch token[i] {
		case '\\', '"', '$', '`':
			b.WriteByte('\\')
		}
		b.WriteByte(token[i])
	}
	return b.String()
}

func cleanValue(v string) string {
	v = strings.TrimSpace(v)
	// Strip an unquoted trailing comment.
	if !strings.HasPrefix(v, `"`) && !strings.HasPrefix(v, "'") {
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
	}
	v = strings.Trim(v, `"'`)
	return v
}

package store

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteFileURI(t *testing.T) {
	// Test Windows paths on every CI host, not only on Windows.
	tests := []struct {
		name string
		path string
		want string
	}{
		{"windows drive", "C:/Users/Alice/postern.db", "file:///C:/Users/Alice/postern.db"},
		{"windows lowercase drive", "d:/data/postern.db", "file:///d:/data/postern.db"},
		{"windows escaped", "C:/Users/Alice Smith/work #1%/postern.db",
			"file:///C:/Users/Alice%20Smith/work%20%231%25/postern.db"},
		{"already prefixed", "/C:/data/postern.db", "file:///C:/data/postern.db"},
		{"unix absolute", "/home/alice/postern.db", "file:///home/alice/postern.db"},
		{"unix escaped", "/home/alice/work #1%?/postern.db",
			"file:///home/alice/work%20%231%25%3F/postern.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sqliteFileURI(tt.path)
			if got != tt.want {
				t.Fatalf("URI = %q, want %q", got, tt.want)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
				t.Fatalf("filesystem path became an authority/query/fragment: %+v", u)
			}
			path := tt.path
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			if u.Path != path {
				t.Fatalf("decoded path = %q, want %q", u.Path, path)
			}
		})
	}
}

func TestOpenDiskPathAndReopen(t *testing.T) {
	for _, directory := range []string{"workspace", "space # percent % and ü"} {
		t.Run(directory, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), directory, "postern.db")
			st, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSetting("test.persistence", "preserved"); err != nil {
				st.Close()
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("database was not created at the requested path: %v", err)
			}
			st, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if got := st.Setting("test.persistence"); got != "preserved" {
				t.Fatalf("reopened setting = %q", got)
			}
		})
	}
}

func TestOpenInMemory(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.ListWorkspaces(); err != nil {
		t.Fatal(err)
	}
}

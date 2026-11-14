// Package brand holds the product identity. Every value can be replaced at
// build time with -ldflags, which is how white-label builds are produced:
//
//	go build -ldflags "-X yourpm/core/brand.Name=AcmeAPI \
//	  -X yourpm/core/brand.DefaultServer=https://sync.acme.example" ./app
package brand

var (
	// Name is the product name shown in the window title and the UI.
	Name = "Postern"
	// Slug is the lowercase identifier used for data directories and binaries.
	Slug = "postern"
	// Version is injected by the release build.
	Version = "0.1.0"
	// DefaultServer is the pre-filled sync server for this build.
	DefaultServer = ""
	// Tagline is shown in the empty state and settings screen.
	Tagline = "Native, private API client"
	// Accent is the default accent colour as #RRGGBB.
	Accent = "#D9541E"
	// Website is displayed in the about section. Empty means hidden.
	Website = ""
	// Logo is an optional base64-encoded PNG injected by brandgen.
	Logo = ""
)

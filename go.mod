module github.com/dibbla-agents/dibbla-cli

// Compatibility floor for
// `go install github.com/dibbla-agents/dibbla-cli/cmd/dibbla@latest`, and the
// version actions/setup-go installs for CI and the release build.
//
// It sits at 1.25.0 because the patched golang.org/x/crypto, x/text and x/sys
// all declare `go 1.25.0`; holding it at 1.24 would have meant shipping 13
// known x/crypto CVEs. GOTOOLCHAIN defaults to `auto`, so users on an older
// Go still install fine — they just auto-fetch a newer toolchain.
//
// Deliberately no `toolchain` directive: Snyk's go.mod parser rejects it
// ("Failed to process go.mod"), which errors the security check on every PR.
// Verified gofmt 1.25.13 and 1.26.6 agree on this tree, so building CI on
// 1.25.0 while dev machines run newer does not destabilise the format gate.
go 1.25.0

require (
	github.com/AlecAivazis/survey/v2 v2.3.7
	github.com/Masterminds/semver/v3 v3.3.1
	github.com/joho/godotenv v1.5.1
	github.com/mattn/go-isatty v0.0.20
	github.com/minio/selfupdate v0.6.0
	github.com/spf13/cobra v1.8.1
	github.com/zalando/go-keyring v0.2.6
	gopkg.in/yaml.v3 v3.0.1
)

require (
	aead.dev/minisign v0.2.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
)

require (
	al.essio.dev/pkg/shellescape v1.5.1 // indirect
	github.com/danieljoos/wincred v1.2.2 // indirect
	github.com/dibbla-agents/dibbla-tasks v0.1.1
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	gopkg.in/check.v1 v1.0.0-20200902074654-038fdea0a05b // indirect
)

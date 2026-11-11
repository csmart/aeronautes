module github.com/csmart/aeronautes

go 1.27.0

require (
	github.com/adrg/xdg v0.5.3
	github.com/godbus/dbus/v5 v5.2.4
	github.com/ncw/swift/v2 v2.0.5
	github.com/wailsapp/wails/v3 v3.2.0-nightly.20261103
	github.com/zalando/go-keyring v0.2.8
	golang.org/x/sys v0.50.0
	golang.org/x/text v0.43.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.62.0
)

require (
	github.com/coder/websocket v1.8.14 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/term v0.46.0 // indirect
	modernc.org/libc v1.80.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/ncw/swift/v2 => ./third_party/ncw-swift/source

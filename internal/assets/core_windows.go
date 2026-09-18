//go:build windows

package assets

import _ "embed"

// fg-core 即更名后的 sing-box 核心（Windows amd64）。
//
//go:embed assets/fg-core-windows-amd64.exe
var coreBin []byte

// wintun.dll 为 Windows TUN 虚拟网卡驱动。
//
//go:embed assets/wintun.dll
var wintunPayload []byte

const coreName = "fg-core.exe"

const coreAvailable = true

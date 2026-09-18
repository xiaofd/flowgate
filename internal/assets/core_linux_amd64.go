//go:build linux && amd64

package assets

import _ "embed"

// fg-core 即更名后的 sing-box 核心（Linux amd64）。
//
//go:embed assets/fg-core-linux-amd64
var coreBin []byte

// Linux 无需 wintun 驱动。
var wintunPayload []byte

const coreName = "fg-core"

const coreAvailable = true

//go:build !windows && (!linux || (!amd64 && !arm64))

package assets

// 其他平台暂不内嵌核心。
var coreBin []byte

var wintunPayload []byte

const coreName = "fg-core"

const coreAvailable = false

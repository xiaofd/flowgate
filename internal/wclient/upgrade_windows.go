//go:build windows

package wclient

import "fmt"

// Upgrade 升级官方客户端。Windows 版自带自动更新器（默认开启，静默自升级），
// 无需也不应通过重装 MSI 的方式升级，此处仅返回当前版本信息。
func Upgrade(cli string) (string, error) {
	return fmt.Sprintf("Windows 客户端自带自动更新（当前 %s）", Version(cli)), nil
}

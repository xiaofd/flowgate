// Package assets 管理内嵌资产（sing-box 核心、wintun 驱动、分流规则集）的释放与更新。
package assets

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed assets/rules/*.srs
var rulesFS embed.FS

// CoreName 返回当前平台核心程序的文件名。
func CoreName() string { return coreName }

// CoreAvailable 当前平台是否内嵌了核心程序。
func CoreAvailable() bool { return coreAvailable }

// RuleNames 返回内嵌规则集标签（去掉 .srs 后缀）。
func RuleNames() []string {
	entries, err := rulesFS.ReadDir("assets/rules")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".srs" {
			continue
		}
		names = append(names, nameOfRule(e.Name()))
	}
	return names
}

func nameOfRule(file string) string { return file[:len(file)-len(".srs")] }

// ExtractRules 将内嵌规则集释放到 dir，返回本次写入的文件名。
func ExtractRules(dir string, force bool) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	entries, err := rulesFS.ReadDir("assets/rules")
	if err != nil {
		return nil, err
	}
	var written []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".srs" {
			continue
		}
		data, err := rulesFS.ReadFile("assets/rules/" + e.Name())
		if err != nil {
			return written, err
		}
		dst := filepath.Join(dir, e.Name())
		if !force && fileMatches(dst, data) {
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return written, err
		}
		written = append(written, e.Name())
	}
	return written, nil
}

// ExtractCore 将当前平台核心释放到 path。返回是否实际写入。
func ExtractCore(path string, force bool) (bool, error) {
	if !coreAvailable {
		return false, fmt.Errorf("当前平台(%s/%s)未内嵌核心程序", runtime.GOOS, runtime.GOARCH)
	}
	if !force && fileMatches(path, coreBin) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, coreBin, 0o755); err != nil {
		return false, err
	}
	return true, nil
}

// ExtractWintun 释放 wintun.dll（仅 Windows 有内嵌数据）。返回是否实际写入。
func ExtractWintun(path string, force bool) (bool, error) {
	if len(wintunPayload) == 0 {
		return false, nil
	}
	if !force && fileMatches(path, wintunPayload) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, wintunPayload, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func fileMatches(path string, data []byte) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Equal(b, data)
}

package config

import (
	"fmt"
	"net"
	"os"
	"strings"
)

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func validateLANHost(host string, allowLoopback bool) error {
	if strings.Contains(host, "/") {
		return fmt.Errorf("config: server-host 只能是 host:port，不能包含路径")
	}
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		return fmt.Errorf("config: server-host 必须是 host:port（得到 %q）: %w", host, err)
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return fmt.Errorf("config: server-host 必须使用可解析 IP（得到 %q）", h)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("config: server-host 不能使用未指定地址（得到 %q）", host)
	}
	if ip.IsLoopback() && !allowLoopback {
		return fmt.Errorf("config: server-host 不能使用回环地址（得到 %q）", host)
	}
	return nil
}

// listenOnLoopback reports whether the listen address itself is loopback-only,
// in which case advertising a loopback server-host is consistent (the only way
// to reach the server is same-host or adb reverse anyway).
func listenOnLoopback(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

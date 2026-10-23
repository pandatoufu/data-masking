//go:build desktop && linux

package desktop

import (
	"errors"

	"github.com/godbus/dbus/v5"
)

// checkSessionBus 确认存在可用的 session bus 且名称服务可达。
// 桌面单实例依赖 session bus；不可用时必须以中文提示结束启动，不静默多开。
func checkSessionBus() error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	if conn == nil {
		return errors.New("session bus 不可用")
	}
	obj := conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	var hasOwner bool
	if err := obj.Call("org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.DBus").Store(&hasOwner); err != nil {
		return err
	}
	if !hasOwner {
		return errors.New("session bus 名称服务不可访问")
	}
	return nil
}

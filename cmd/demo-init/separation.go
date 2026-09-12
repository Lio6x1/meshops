package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func browserSecretNames() []string {
	return []string{"MESHOPS_WEB_OPERATOR_CODE", "MESHOPS_WEB_ADMIN_CODE"}
}
func runtimeSecretNames() []string {
	var names []string
	for _, name := range secretNames() {
		if strings.HasSuffix(name, "_TOKEN") || name == "MESHOPS_CURSOR_KEY" || name == "MESHOPS_MYSQL_PASSWORD" {
			names = append(names, name)
		}
	}
	return names
}

// 共享数据卷不代表共享读取权限。完整初始化凭证
// 归 root 所有；应用组只能读取可信 Registry 的必要子集。
// 网关使用独立 UID，额外获得浏览器访问码的读取权限。
func ensureDerivedSecrets(dir string, full map[string]string) error {
	for _, item := range []struct {
		name     string
		names    []string
		uid, gid int
		mode     os.FileMode
	}{
		{"runtime-secrets.json", runtimeSecretNames(), 10001, 10001, 0440},
		{"web-codes.json", browserSecretNames(), 10002, 10001, 0400},
	} {
		values := map[string]string{}
		for _, name := range item.names {
			values[name] = full[name]
		}
		data, err := json.MarshalIndent(values, "", "  ")
		if err != nil {
			return err
		}
		if err = ensureDerivedFile(filepath.Join(dir, item.name), data, item.uid, item.gid, item.mode); err != nil {
			return err
		}
	}
	return ensureRootPasswordFile(dir, full["MESHOPS_MYSQL_ROOT_PASSWORD"])
}

func ensureDerivedFile(path string, want []byte, uid, gid int, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("derived credential path is not a regular file")
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if !bytes.Equal(data, want) {
			return fmt.Errorf("derived credential %s differs from durable secrets; restore its original content", filepath.Base(path))
		}
		return protectFile(path, uid, gid, mode)
	}
	if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(want); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return protectFile(path, uid, gid, mode)
}

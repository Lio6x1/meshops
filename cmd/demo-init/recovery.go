package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"example.com/meshops-course/internal/search"
)

func invalidateSearch(dir string) error {
	path := filepath.Join(dir, "search-bootstrap.json")
	if err := search.SaveBootstrap(path, search.Bootstrap{Version: 1, Database: "meshops_course", Index: search.TaskIndexName}); err != nil {
		return err
	}
	return ownSecret(path)
}

// 服务停止由编排层负责；此函数负责维护持久化标记：
// 任何未成功的导入，即使子进程已写入 complete=true，也必须再次使标记失效，
// 避免后续普通 Up 启动时悄悄对外提供不完整的投影。
func rebuildSearchMarker(ctx context.Context, dir string, importSnapshot func(context.Context) error) (err error) {
	if err = invalidateSearch(dir); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, invalidateSearch(dir))
		}
	}()
	if err = importSnapshot(ctx); err != nil {
		return fmt.Errorf("search rebuild failed: %w", err)
	}
	if _, err = search.LoadBootstrap(filepath.Join(dir, "search-bootstrap.json")); err != nil {
		return err
	}
	return ownSecret(filepath.Join(dir, "search-bootstrap.json"))
}

// 实际调用只传入固定挂载路径，不接受用户提供的删除路径，
// 也不递归删除。下列文件对应当前配置使用的
// CanalFileMetaManager 及其 H2 TSDB。删除前先验证整个目录，
// 遇到未知格式立即拒绝，防止维护操作漏掉未识别的游标文件。
func resetCanalMeta(base string) error {
	info, err := os.Lstat(base)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("canal metadata mount is not a real directory")
	}
	destination := filepath.Join(base, "meshops")
	info, err = os.Lstat(destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("canal destination is not a real directory")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "meta.dat", "h2.mv.db", "h2.trace.db", "h2.lock.db":
		default:
			return fmt.Errorf("unknown Canal metadata file %s; inspect before recovery", entry.Name())
		}
		info, e := os.Lstat(filepath.Join(destination, entry.Name()))
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("canal metadata contains a directory or symbolic link")
		}
	}
	for _, entry := range entries {
		if err = os.Remove(filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	// 只删除经过验证的空目录，绝不对数据卷调用 RemoveAll。
	return os.Remove(destination)
}

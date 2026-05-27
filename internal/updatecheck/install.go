package updatecheck

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func InstallBinary(srcPath, targetPath string) error {
	if runtime.GOOS == "windows" {
		return installBinaryWindows(srcPath, targetPath)
	}
	return installBinaryPosix(srcPath, targetPath)
}

func installBinaryPosix(srcPath, targetPath string) error {
	tmpPath := targetPath + ".tmp"
	if err := copyFile(srcPath, tmpPath); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func installBinaryWindows(srcPath, targetPath string) error {
	for i := 0; i < 20; i++ {
		if err := os.Remove(targetPath); err == nil || os.IsNotExist(err) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err := copyFile(srcPath, targetPath); err != nil {
		return fmt.Errorf("copy update: %w", err)
	}
	return nil
}

func copyFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return nil
}

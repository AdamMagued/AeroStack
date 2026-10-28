//go:build !darwin

package machine

import (
	"fmt"
	"io"
	"os"
)

func CloneFile(src, dst string) error {
	return CopySparse(src, dst)
}

func CopySparse(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source: %w", err)
	}
	defer in.Close()

	st, err := in.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source: %w", err)
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}
	defer out.Close()

	buf := make([]byte, 1024*1024)
	for {
		n, rErr := in.Read(buf)
		if n > 0 {
			isZero := true
			for i := 0; i < n; i++ {
				if buf[i] != 0 {
					isZero = false
					break
				}
			}

			if isZero {
				if _, sErr := out.Seek(int64(n), io.SeekCurrent); sErr != nil {
					return fmt.Errorf("seek error: %w", sErr)
				}
			} else {
				if _, wErr := out.Write(buf[:n]); wErr != nil {
					return fmt.Errorf("write error: %w", wErr)
				}
			}
		}

		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", rErr)
		}
	}

	if err := out.Truncate(st.Size()); err != nil {
		return fmt.Errorf("truncate error: %w", err)
	}

	return nil
}

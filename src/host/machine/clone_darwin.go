//go:build darwin && cgo

package machine

/*
#include <sys/clonefile.h>
#include <copyfile.h>
#include <stdlib.h>
#include <errno.h>

static int do_clonefile(const char *src, const char *dst) {
    int ret = clonefile(src, dst, 0);
    if (ret != 0) {
        return errno;
    }
    return 0;
}
*/
import "C"
import (
	"fmt"
	"io"
	"os"
	"unsafe"
)

// CloneFile attempts instant APFS CoW clonefile, falling back to sparse copying
func CloneFile(src, dst string) error {
	cSrc := C.CString(src)
	defer C.free(unsafe.Pointer(cSrc))
	cDst := C.CString(dst)
	defer C.free(unsafe.Pointer(cDst))

	errCode := C.do_clonefile(cSrc, cDst)
	if errCode == 0 {
		return nil
	}

	// On non-APFS volumes or cross-device, fall back to sparse copy
	return CopySparse(src, dst)
}

// CopySparse copies src to dst preserving sparse holes
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

	buf := make([]byte, 1024*1024) // 1MB buffer
	for {
		n, rErr := in.Read(buf)
		if n > 0 {
			// Check if buffer is all zeros (sparse chunk)
			isZero := true
			for i := 0; i < n; i++ {
				if buf[i] != 0 {
					isZero = false
					break
				}
			}

			if isZero {
				// Seek forward without writing to create sparse hole
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

	// Ensure final file size matches
	if err := out.Truncate(st.Size()); err != nil {
		return fmt.Errorf("truncate error: %w", err)
	}

	return nil
}

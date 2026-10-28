//go:build darwin && cgo

package machine

/*
#include <sys/clonefile.h>
#include <copyfile.h>
#include <stdlib.h>
#include <errno.h>
#include <unistd.h>

#ifndef SEEK_DATA
#define SEEK_DATA 4
#endif

#ifndef SEEK_HOLE
#define SEEK_HOLE 5
#endif

static int do_clonefile(const char *src, const char *dst) {
    int ret = clonefile(src, dst, 0);
    if (ret != 0) {
        return errno;
    }
    return 0;
}

static off_t darwin_seek_data(int fd, off_t offset) {
    return lseek(fd, offset, SEEK_DATA);
}

static off_t darwin_seek_hole(int fd, off_t offset) {
    return lseek(fd, offset, SEEK_HOLE);
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

// CopySparse copies src to dst preserving sparse holes using Darwin lseek SEEK_HOLE/SEEK_DATA
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
	fileSize := st.Size()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}
	defer out.Close()

	if fileSize == 0 {
		return nil
	}

	inFd := C.int(in.Fd())
	var offset C.off_t = 0
	buf := make([]byte, 1024*1024) // 1MB buffer

	for offset < C.off_t(fileSize) {
		dataStart := C.darwin_seek_data(inFd, offset)
		if dataStart < 0 {
			if offset == 0 {
				// Filesystem does not support SEEK_DATA/SEEK_HOLE; fall back to streaming copy
				if _, err := in.Seek(0, io.SeekStart); err == nil {
					if _, err := io.Copy(out, in); err == nil {
						return nil
					}
				}
			}
			// No more data segments; rest of file is sparse hole
			break
		}
		if dataStart >= C.off_t(fileSize) {
			break
		}

		holeStart := C.darwin_seek_hole(inFd, dataStart)
		if holeStart < 0 || holeStart > C.off_t(fileSize) {
			holeStart = C.off_t(fileSize)
		}

		// Seek destination and source to dataStart
		if _, err := out.Seek(int64(dataStart), io.SeekStart); err != nil {
			return fmt.Errorf("seek destination error: %w", err)
		}
		if _, err := in.Seek(int64(dataStart), io.SeekStart); err != nil {
			return fmt.Errorf("seek source error: %w", err)
		}

		// Copy data block between dataStart and holeStart
		remaining := int64(holeStart - dataStart)
		for remaining > 0 {
			toRead := int64(len(buf))
			if remaining < toRead {
				toRead = remaining
			}
			n, rErr := in.Read(buf[:toRead])
			if n > 0 {
				if _, wErr := out.Write(buf[:n]); wErr != nil {
					return fmt.Errorf("write error: %w", wErr)
				}
				remaining -= int64(n)
			}
			if rErr != nil {
				if rErr == io.EOF {
					break
				}
				return fmt.Errorf("read error: %w", rErr)
			}
		}

		offset = holeStart
	}

	// Ensure final destination file size matches exactly
	if err := out.Truncate(fileSize); err != nil {
		return fmt.Errorf("truncate error: %w", err)
	}

	return nil
}

package smriti

import (
	"syscall"
)

// mmap allocates a region of memory of the given size using syscall.Mmap.
// It requests an anonymous, private, readable, and writable mapping.
// Returns a []byte slice backed by the mmap'd memory.
func allocate(size int) ([]byte, error) {
	if size <= 0 {
		return nil, syscall.EINVAL // Invalid argument error
	}

	// syscall.Mmap parameters:
	// MAP_ANONYMOUS: The mapping is not backed by any file; its contents are initialized to zero.
	// MAP_PRIVATE: Create a private copy-on-write mapping. Updates to the mapping are not visible to other processes mapping the same file.
	// PROT_READ | PROT_WRITE: Pages may be read and written.
	return syscall.Mmap(
		-1,   // fd: -1 for anonymous mapping
		0,    // offset: 0
		size, // length
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANONYMOUS|syscall.MAP_PRIVATE,
	)
}

// munmap unmaps a previously mmap'd region of memory.
// It takes the []byte slice that was returned by mmap.
func free(data []byte) error {
	if len(data) == 0 {
		return syscall.EINVAL // Invalid argument error
	}

	// syscall.Munmap unmaps the memory region.
	return syscall.Munmap(data)
}

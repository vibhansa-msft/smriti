package smriti

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	// LazyDLL and NewProc are used for dynamically loading kernel32.dll functions.
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procVirtualAlloc = modkernel32.NewProc("VirtualAlloc")
	procVirtualFree  = modkernel32.NewProc("VirtualFree")
)

const (
	// MEM_COMMIT: Allocates memory pages and marks them as committed.
	MEM_COMMIT = 0x1000
	// MEM_RELEASE: Decommits and releases the specified region of pages.
	MEM_RELEASE = 0x8000
	// PAGE_READWRITE: Enables read and write access to the committed region of pages.
	PAGE_READWRITE = 0x04
)

// mmap allocates a region of memory of the given size using VirtualAlloc on Windows.
// Returns a []byte slice backed by the allocated memory.
func allocate(size int) ([]byte, error) {
	if size == 0 {
		return nil, syscall.EINVAL // Invalid argument error
	}

	// VirtualAlloc reserves or commits a region of pages in the virtual address space
	// of the calling process.
	addr, _, err := procVirtualAlloc.Call(
		0,              // lpAddress: desired starting address (NULL for OS to determine)
		uintptr(size),  // dwSize: size of the region to allocate
		MEM_COMMIT,     // flAllocationType: allocate and commit pages
		PAGE_READWRITE, // flProtect: page protection (read/write access)
	)

	if addr == 0 {
		return nil, fmt.Errorf("VirtualAlloc failed: %v", err)
	}

	// Create a Go slice from the allocated memory address.
	// This uses unsafe.Pointer to convert the raw memory address to a Go slice header.
	var sl = struct {
		addr uintptr
		len  int
		cap  int
	}{addr, size, size}
	return *(*[]byte)(unsafe.Pointer(&sl)), nil
}

// munmap frees a previously allocated region of memory using VirtualFree on Windows.
// It takes the []byte slice that was returned by mmap.
func free(data []byte) error {
	if data == nil || len(data) == 0 {
		return syscall.EINVAL // Invalid argument error
	}

	// Get the base address of the Go slice.
	addr := uintptr(unsafe.Pointer(&data[0]))

	// VirtualFree decommits and releases a region of pages.
	// dwSize is 0 when MEM_RELEASE is used, meaning the entire region allocated by VirtualAlloc
	// at lpAddress is released.
	ret, _, err := procVirtualFree.Call(
		addr,        // lpAddress: base address of the region to free
		0,           // dwSize: 0 when MEM_RELEASE is used
		MEM_RELEASE, // dwFreeType: release pages
	)

	if ret == 0 {
		return fmt.Errorf("VirtualFree failed: %v", err)
	}

	return nil
}

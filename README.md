# Smriti

When any GoLang application allocates memory dynamically, it allocated on the heap. Go is GCed, which means memory allocated dynamicallly is not freed by the application rather collected later by the Garbadge Collector. Go GC works in a very peculiar fashion, where application can set the GC percentage and number of CPUs to use. During each iteration of GC, golang checks the amount of memory usage increase compared to last run. If the increase is beyond the configured percentage then the GC is hit. GC will scan through the entire allocated memory space to figure out the blocks of memory that are no longer in use and can be freed. This process can be done in parallel, in different regions of memory and the parallelism factor is controlled by the number of CPUs configured.

This entire process has a huge bottleneck that, when GC runs all the Go Routines are stalled. If the application is memory heavy then there will be a large memory set to be scanned which will take some time. Thus GC results into overall perforamnce degradation of the application.

This shall not be considered as a general purpose memory manager as it allocates a fixed size blocks, where size and total amount of memory that it can scale is configurable.


### Key features of this memory manager:

#### mmap for Memory Allocation: 

Uses syscall.Mmap (Linux) or VirtualAlloc (Windows) to allocate memory directly from the operating system, bypassing the Go runtime's heap and garbage collector for the actual memory blocks.

#### Dynamic Scaling:

Expansion: If 80% of currently allocated blocks are in use, it allocates an additional 10% of the maxBlockCount until the maxBlockCount is reached.

#### Shrinking: 

If more than 50% of the allocated blocks are free (i.e., the memory pool is underutilized) it deallocates 10% of the allocated blocks until the initialBlockCount is reached.

#### Concurrency Safe: 

Uses Go channels (availableBlocks, returnedBlocks) for safe concurrent access to blocks and a sync.Mutex to protect shared state (currentAllocatedCount, allMappedBlocks).

#### Idle Detection: 

A time.Timer is used to detect prolonged periods of inactivity, triggering the shrinking mechanism every 1 minute.

#### Graceful Shutdown: 

The Close() method ensures all background goroutines are stopped and all mmap'd memory is properly munmap'd to prevent memory leaks.

GC Avoidance: By storing the []byte slices returned by mmap in the allMappedBlocks map (keyed by their underlying memory address), the Go runtime maintains a reference to these slices, preventing their headers from being garbage collected while the mmap'd memory is still active. This allows explicit munmap calls when blocks are deallocated.
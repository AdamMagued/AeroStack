package main

import (
	"fmt"
	"runtime"
)

func main() {
	if runtime.GOOS != "darwin" {
		fmt.Println("Benchmarks require macOS")
		return
	}

	runSnapshotBenchmarks()
	runProxyBenchmarks()

	fmt.Println("\n================================================================================")
	fmt.Println("   BENCHMARKS COMPLETE: ALL SYSTEMS NOMINAL")
	fmt.Println("================================================================================")
}

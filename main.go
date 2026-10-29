package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: llmmeter serve [-db llmmeter.db] [-listen 127.0.0.1:8100]")
	os.Exit(2)
}

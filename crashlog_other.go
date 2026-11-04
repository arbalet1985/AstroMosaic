//go:build !windows

package main

import (
	"fmt"
	"os"
)

func showLogError(err error) { fmt.Fprintln(os.Stderr, err) }

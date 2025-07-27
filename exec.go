package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func git(args ...string) (string, error)  { return execCmd("git", args...) }
func gh(args ...string) (string, error)   { return execCmd("gh", args...) }
func jj(args ...string) (string, error)   { return execCmd("jj", args...) }
func _git(args ...string) (string, error) { return _execCmd("git", args...) }
func _gh(args ...string) (string, error)  { return _execCmd("gh", args...) }
func _jj(args ...string) (string, error)  { return _execCmd("jj", args...) }

func execCmd(name string, args ...string) (string, error) {
	output, err := _execCmd(name, args...)
	if err != nil && !config.verbose {
		fmt.Println(output)
	}
	return output, err
}

func _execCmd(name string, args ...string) (string, error) {
	if config.verbose {
		fmt.Print(name, " ")
		for _, arg := range args {
			if strings.Contains(arg, " ") {
				fmt.Printf("%q", arg)
			} else {
				fmt.Print(arg, " ")
			}
		}
		fmt.Println()
	}
	output, err := exec.Command(name, args...).CombinedOutput()
	if config.verbose {
		fmt.Println(string(output))
	}
	return string(output), err
}

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var editor = os.Getenv("EDITOR")
var zetDir = "./zettel"
var defaultPrefix = "tmp"

func main() {
	log.SetFlags(0) // turn off timestamping log statements, this is a cli app

	err := os.MkdirAll(zetDir, os.ModePerm) // ensures existence of zettel dir
	if err != nil {
		log.Fatalf("Unable to ensure zettel dir '%s': %s", zetDir, err)
	}

	shift(&os.Args)
	if len(os.Args) == 0 {
		// NOTE: shorthand for create with default prefix
		CreateCommand(defaultPrefix)
		return
	}

	if len(os.Args) == 1 {
		// NOTE: support for shorthand prefixes

		// add checks for supported singular commands here

		CreateCommand(os.Args[0])
	}

	// TODO: subcommand tree
	switch os.Args[0] {
	}
}

// TODO: branch command
// TODO: extract command
// TODO: link command
//	- xclip on linux, pbcopy on darwin, ??? on windows
// TODO: open command
//	- w/ support for xxx.1a2b -> xxx.1a2b1.md

// TODO: help/usage output
// TODO: tab completion

// TODO: goreleaser

// 1.0 here

// further features past 1.0
// TODO: rename command
// TODO: graft command
// TODO: prune command

func CreateCommand(prefix string) {

	entries, err := os.ReadDir(zetDir)
	if err != nil {
		log.Fatalf("Unable to read zettel dir '%s': %s", zetDir, err)
	}

	maxNum := 0
	dotSeparated := true
	numberPrefix := unicode.IsDigit(rune(prefix[len(prefix)-1]))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		suffix, found := strings.CutPrefix(e.Name(), prefix)
		if !found {
			continue
		}

		if suffix[0] == '.' {
			suffix = suffix[1:]
		} else {
			if !numberPrefix {
				dotSeparated = false
			}
		}

		if !unicode.IsDigit(rune(suffix[0])) {
			continue
		}

		id, _ := strings.CutSuffix(suffix, ".md")

		num, err := strconv.Atoi(id)
		if err != nil {
			log.Printf("unable to parse number: %s", err)
		}
		if num > maxNum {
			maxNum = num
		}
	}

	var zettelId string
	if dotSeparated {
		zettelId = fmt.Sprintf("%s.%d", prefix, maxNum+1)
	} else {
		zettelId = fmt.Sprintf("%s%d", prefix, maxNum+1)
	}

	fileName := fmt.Sprintf("%s.md", zettelId)
	filePath := path.Join(zetDir, fileName)
	f, err := os.Create(filePath)
	if err != nil {
		log.Fatalf("Unable to create file '%s': %s", filePath, err)
	}

	ts := timestamp()

	content := fmt.Sprintf("---\nzettel: %s\ndate: %s\n---\n\n\n\n", zettelId, ts)
	f.Write([]byte(content))
	f.Close()

	openInEditor(filePath)
}

func openInEditor(path string) {
	cmd := exec.Command(editor, "+6", path)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	err := cmd.Run()
	if err != nil {
		log.Fatalf("Unable to run editor (%s) command: %s", editor, err)
	}
}

func timestamp() string {
	format := "Mon 2006-01-02 15:04:05 MST"
	now := time.Now()
	ts := now.Format(format)
	return ts
}

func shift(stringSlice *[]string) {
	*stringSlice = (*stringSlice)[1:]
}

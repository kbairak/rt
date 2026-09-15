package main

import (
	"strings"
	"testing"
)

func TestBlockParserSingle(t *testing.T) {
	store := NewBlockStore()
	cmds := NewCommandQueue()
	p := newBlockParser(store, cmds)

	cmds.Push("ls")
	p.Feed([]byte("prompt noise RTMRK$ ls\nfile1  file2\nRTMRK$ "))

	blocks := store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Command != "ls" {
		t.Fatalf("expected command 'ls', got %q", blocks[0].Command)
	}
	if !strings.Contains(blocks[0].Output, "file1") {
		t.Fatalf("expected output containing 'file1', got %q", blocks[0].Output)
	}
}

func TestBlockParserMultiple(t *testing.T) {
	store := NewBlockStore()
	cmds := NewCommandQueue()
	p := newBlockParser(store, cmds)

	cmds.Push("ls")
	cmds.Push("pwd")
	p.Feed([]byte("RTMRK$ ls\nfile1\nRTMRK$ pwd\n/home/user\nRTMRK$ "))

	blocks := store.All()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Command != "ls" {
		t.Fatalf("block 0 command: expected 'ls', got %q", blocks[0].Command)
	}
	if blocks[1].Command != "pwd" {
		t.Fatalf("block 1 command: expected 'pwd', got %q", blocks[1].Command)
	}
}

func TestBlockParserSplitChunks(t *testing.T) {
	store := NewBlockStore()
	cmds := NewCommandQueue()
	p := newBlockParser(store, cmds)

	cmds.Push("ls")
	p.Feed([]byte("garbageRTMRK$ ls\nhel"))
	p.Feed([]byte("lo\nRTMRK$ "))

	blocks := store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Command != "ls" {
		t.Fatalf("expected command 'ls', got %q", blocks[0].Command)
	}
	if blocks[0].Output != "$ ls\nhello" {
		t.Fatalf("expected output '$ ls\\nhello', got %q", blocks[0].Output)
	}
}

func TestBlockParserSkipsInitialNoise(t *testing.T) {
	store := NewBlockStore()
	cmds := NewCommandQueue()
	p := newBlockParser(store, cmds)

	cmds.Push("ls")
	p.Feed([]byte("some startup noise from shell RTMRK$ ls\noutput\nRTMRK$ "))

	blocks := store.All()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Command != "ls" {
		t.Fatalf("expected command 'ls', got %q", blocks[0].Command)
	}
}
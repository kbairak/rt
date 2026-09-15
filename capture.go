package main

import (
	"bytes"
	"strings"
	"sync"
)

type BlockStore struct {
	mu     sync.Mutex
	blocks []Block
}

func NewBlockStore() *BlockStore {
	return &BlockStore{}
}

func (bs *BlockStore) Add(b Block) {
	bs.mu.Lock()
	bs.blocks = append(bs.blocks, b)
	bs.mu.Unlock()
}

func (bs *BlockStore) All() []Block {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	out := make([]Block, len(bs.blocks))
	copy(out, bs.blocks)
	return out
}

func (bs *BlockStore) Len() int {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return len(bs.blocks)
}

type CommandQueue struct {
	mu   sync.Mutex
	cmds []string
}

func NewCommandQueue() *CommandQueue {
	return &CommandQueue{}
}

func (q *CommandQueue) Push(cmd string) {
	q.mu.Lock()
	q.cmds = append(q.cmds, cmd)
	q.mu.Unlock()
}

func (q *CommandQueue) Pop() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.cmds) == 0 {
		return ""
	}
	cmd := q.cmds[0]
	q.cmds = q.cmds[1:]
	return cmd
}

var marker = []byte("RTMRK")

type blockParser struct {
	buf       bytes.Buffer
	seenFirst bool
	store     *BlockStore
	cmds      *CommandQueue
	partial   []byte
}

func newBlockParser(store *BlockStore, cmds *CommandQueue) *blockParser {
	return &blockParser{store: store, cmds: cmds}
}

func (p *blockParser) Feed(chunk []byte) {
	data := chunk
	if len(p.partial) > 0 {
		data = append(p.partial, chunk...)
		p.partial = nil
	}

	i := 0
	for i < len(data) {
		if bytes.HasPrefix(data[i:], marker) {
			if p.seenFirst {
				out := strings.TrimRight(p.buf.String(), "\n\r ")
				cmd := p.cmds.Pop()
				p.store.Add(Block{
					Command:  cmd,
					Output:   out,
					ExitCode: 0,
				})
			} else {
				p.seenFirst = true
			}
			p.buf.Reset()
			i += len(marker)
			continue
		}
		p.buf.WriteByte(data[i])
		i++
	}

	// save partial marker at end of data
	for j := len(data) - len(marker) + 1; j < len(data); j++ {
		if j < 0 {
			continue
		}
		suffix := data[j:]
		if hasPartialPrefix(marker, suffix) {
			p.partial = append([]byte(nil), suffix...)
			break
		}
	}
}

func hasPartialPrefix(pat, s []byte) bool {
	if len(s) >= len(pat) {
		return bytes.HasPrefix(s, pat)
	}
	return bytes.HasPrefix(pat, s)
}
package perm

import (
	"fmt"
	"strings"
)

// diffOp is one line of a computed diff: kept, deleted, or inserted.
type diffOp struct {
	kind byte // ' ' keep, '-' delete, '+' insert
	line string
}

// UnifiedDiff renders a unified diff (3 context lines) between two contents.
// Empty string means the contents are identical.
func UnifiedDiff(name string, oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	ops := myersDiff(splitDiffLines(oldText), splitDiffLines(newText))
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", name, name)
	writeHunks(&b, ops, 3)
	return b.String()
}

// splitDiffLines splits content into lines without trailing newlines; a
// trailing newline does not create a phantom empty line.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// myersDiff is the classic O(ND) greedy diff over lines, returning the edit
// script in order.
func myersDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}
	// v[k] = furthest x on diagonal k (offset by max); trace keeps one copy
	// per step for backtracking.
	v := make([]int, 2*max+2)
	var trace [][]int
	var dFound = -1
outer:
	for d := 0; d <= max; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[max+k-1] < v[max+k+1]) {
				x = v[max+k+1] // move down (insert from b)
			} else {
				x = v[max+k-1] + 1 // move right (delete from a)
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[max+k] = x
			if x >= n && y >= m {
				dFound = d
				break outer
			}
		}
	}

	// Backtrack from (n,m) through the saved traces, collecting ops reversed.
	var rev []diffOp
	x, y := n, m
	for d := dFound; d > 0; d-- {
		vPrev := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vPrev[max+k-1] < vPrev[max+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vPrev[max+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x, y = x-1, y-1
			rev = append(rev, diffOp{' ', a[x]})
		}
		if x == prevX {
			y--
			rev = append(rev, diffOp{'+', b[y]})
		} else {
			x--
			rev = append(rev, diffOp{'-', a[x]})
		}
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		rev = append(rev, diffOp{' ', a[x]})
	}

	ops := make([]diffOp, len(rev))
	for i, op := range rev {
		ops[len(rev)-1-i] = op
	}
	return ops
}

// writeHunks groups ops into @@ hunks with the given context width.
func writeHunks(b *strings.Builder, ops []diffOp, ctx int) {
	oldLine, newLine := 1, 1
	i := 0
	for i < len(ops) {
		// Skip runs of unchanged lines to find the next change.
		if ops[i].kind == ' ' {
			oldLine, newLine = oldLine+1, newLine+1
			i++
			continue
		}
		// Hunk starts ctx lines before this change.
		start := i - ctx
		if start < 0 {
			start = 0
		}
		lead := i - start
		hunkOldStart, hunkNewStart := oldLine-lead, newLine-lead

		// Extend the hunk until a gap of >2*ctx unchanged lines (or the end).
		end := i
		gap := 0
		for j := i; j < len(ops); j++ {
			if ops[j].kind == ' ' {
				gap++
				if gap > 2*ctx {
					break
				}
			} else {
				gap = 0
				end = j
			}
		}
		stop := end + 1 + ctx
		if stop > len(ops) {
			stop = len(ops)
		}

		oldCount, newCount := 0, 0
		var body strings.Builder
		for j := start; j < stop; j++ {
			body.WriteByte(ops[j].kind)
			body.WriteString(ops[j].line)
			body.WriteByte('\n')
			switch ops[j].kind {
			case ' ':
				oldCount, newCount = oldCount+1, newCount+1
				if j >= i {
					oldLine, newLine = oldLine+1, newLine+1
				}
			case '-':
				oldCount++
				if j >= i {
					oldLine++
				}
			case '+':
				newCount++
				if j >= i {
					newLine++
				}
			}
		}
		fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", hunkOldStart, oldCount, hunkNewStart, newCount)
		b.WriteString(body.String())
		i = stop
	}
}

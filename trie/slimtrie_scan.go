package trie

import (
	"github.com/openacid/low/bitmap"
)

// NextRaw returns next key-value pair in []byte.
type NextRaw func() ([]byte, []byte)

// Scan from a specified key. It returns a function `next()` that yields next key and value every time called.
// The `next()` returns nil after all keys yield.
// The key and value it yields is a tmp slice []byte, i.e., next time calling the `next()`, the previously returned slice will be invalid.
//
// Since 0.5.11
func (st *SlimTrie) Scan(key string, withValue bool) NextRaw {
	path := st.getGEPath(key)
	return st.scan(path, withValue)
}

func (st *SlimTrie) scan(path []int32, withValue bool) NextRaw {

	buf := make([]byte, 0, 64)
	bufBitIdx := int32(0)
	stack := make([]scanStackElt, len(path)*2)
	stackIdx := -1
	for i := int32(0); i < int32(len(path)-1); i++ {

		qr := &querySession{}
		st.getNode(path[i], qr)

		stackIdx++
		v := &stack[stackIdx]
		v.init(st, path[i], path[i+1], qr, bufBitIdx)
		v.appendInnerPrefix(&buf, qr)

		// NOTE: the first time executing next(), the last label will always be overridden.
		v.appendLabel(&buf)
		bufBitIdx = v.labelEnd
	}

	return func() ([]byte, []byte) {

		if stackIdx == -1 {
			return nil, nil
		}

		var val []byte

		// walk to a leaf and fill in the buf
		for {
			last := &stack[stackIdx]
			last.appendLabel(&buf)

			childId := last.firstChildId + last.ithLabel
			qr := &querySession{}
			st.getNode(childId, qr)
			if !qr.isInner {
				last.appendLeafPrefix(&buf, qr)
				if withValue {
					leafI, _ := st.getLeafIndex(childId)
					val = st.getIthLeafBytes(leafI)
				}
				break
			}

			stackIdx++
			if stackIdx == len(stack) {
				stack = append(stack, scanStackElt{})
			}
			elt := &stack[stackIdx]
			elt.init(st, childId, -1, qr, last.labelEnd)
			elt.appendInnerPrefix(&buf, qr)
		}

		// remove leaf from the stack and walk to next.
		for stackIdx >= 0 {
			last := &stack[stackIdx]
			labelSz := last.nextLabel(1)
			if labelSz != -1 {
				break
			}
			stackIdx--
		}
		return buf, val
	}
}

// getGEPath finds the node path in the trie from root to a leaf, that represents a string >= key
func (st *SlimTrie) getGEPath(key string) []int32 {

	if st.inner.NodeTypeBM == nil {
		return []int32{}
	}

	if st.inner.InnerPrefixes == nil || st.inner.LeafPrefixes == nil {
		panic("incomplete slim does not support scanning. requires InnerPrefixes and LeafPrefixes")
	}

	eqID := int32(0)
	// the smallest child id ever seen that is greater than key.
	rID := int32(-1)
	// the length of the right side path.
	rightPathLen := int32(-1)
	l := int32(8 * len(key))
	path := make([]int32, 0)
	ns := st.inner

	qr := &querySession{
		keyBitLen: l,
		key:       key,
	}

	i := int32(0)

	for {

		st.getNode(eqID, qr)
		if !qr.isInner {
			// leaf
			break
		}

		if qr.hasPrefixContent {
			r := prefixCompare(key[i>>3:], qr.prefix)
			if r == 0 {
				i = i&(^7) + qr.prefixLen
			} else if r < 0 {
				rID = eqID
				rightPathLen = int32(len(path))
				eqID = -1
				break
			} else {
				// choose the next smallest path
				eqID = -1
				break
			}
		}

		path = append(path, eqID)

		leftChild, has := st.getLeftChildID(qr, i)
		chID := leftChild + has
		rightChild := chID + 1

		rightMostChild, bit := bitmap.Rank128(ns.Inners.Words, ns.Inners.RankIndex, qr.to-1)
		rightMostChild += bit

		if rightChild <= rightMostChild {
			rID = rightChild
			rightPathLen = int32(len(path))
		}

		if has == 0 {
			eqID = -1
			break
		}
		eqID = chID

		// quick path: leaf has no prefix. qr.wordSize is 0. matches the 0-th bit
		if i == l {
			// must be a leaf
			break
		}

		i += qr.wordSize
	}

	if eqID != -1 {
		tail := key[i>>3:]
		r := st.cmpLeafPrefix(tail, qr)
		if r <= 0 {
			path = append(path, eqID)
			return path
		}
	}

	if rID == -1 {
		return []int32{}
	}

	// discard the exact-match part, choose the next smallest path
	path = path[:rightPathLen]
	st.leftMost(rID, &path)

	return path
}

// scanStackElt represents the recursion state of a node.
type scanStackElt struct {
	st           *SlimTrie
	nodeId       int32
	firstChildId int32
	ithLabel     int32

	// labelBit is the index of the label in a inner node bitmap
	labelBit int32

	// labelWidth is 0, 4 or 8
	labelWidth int32
	// label is a 0-bit, 4-bit or 8-bit word
	label int32

	// bit range of this inner node.
	bitFrom, bitTo int32
	// If it is a short bitmap, cache it.
	bm uint64

	// The buffer is in form of <prefix><label><prefix><label>... these 3 fields are bit index in the buf.
	prefixStart int32
	prefixEnd   int32
	labelEnd    int32
}

func (v *scanStackElt) init(st *SlimTrie, parentId, childId int32, qr *querySession, bufBitIdx int32) {

	ns := st.inner
	prefStart := bufBitIdx
	prefEnd := prefStart
	if qr.hasPrefixContent {
		prefEnd = bufBitIdx&(^7) + qr.prefixLen
	}

	// childId = rank_inclusive(globalLabelBitIdx)
	//         = rank_exclusive(qr.from) + ithBit + 1
	// ithBit = childId - 1 - rank_exclusive(qr.from)
	rnk, _ := bitmap.Rank128(ns.Inners.Words, ns.Inners.RankIndex, qr.from)
	firstChildId := rnk + 1

	labelIdx := childId - firstChildId
	// childId=-1 to find the first childId
	if childId == -1 {
		labelIdx = 0
	}

	v.st = st
	v.nodeId = parentId
	v.firstChildId = firstChildId
	v.ithLabel = labelIdx - 1
	v.bitFrom = qr.from
	v.bitTo = qr.to
	v.bm = 0
	v.labelBit = -1
	v.labelWidth = -1
	v.label = -1
	v.prefixStart = prefStart
	v.prefixEnd = prefEnd

	if v.bitTo-v.bitFrom == ns.ShortSize {
		v.bm = qr.bm
	}

	v.nextLabel(labelIdx + 1)
}

func (v *scanStackElt) nextLabelBit(n int32) int32 {
	for n > 0 {
		v.labelBit++
		if v.bm != 0 {
			if v.labelBit == 17 {
				return -1
			}
			if v.bm&bitmap.Bit[v.labelBit] != 0 {
				n--
			}
		} else {
			if v.labelBit == v.bitTo-v.bitFrom {
				return -1
			}
			i := v.bitFrom + v.labelBit
			if v.st.inner.Inners.Words[i>>6]&bitmap.Bit[i&63] != 0 {
				n--
			}
		}
	}
	return v.labelBit
}

// move cursor to next label
func (v *scanStackElt) nextLabel(n int32) int32 {
	v.ithLabel++

	labelBit := v.nextLabelBit(n)
	if labelBit == -1 {
		return -1
	}
	v.updateLabel()
	return v.labelWidth
}

// update the size and label
func (v *scanStackElt) updateLabel() {
	if v.labelBit == 0 {
		v.labelWidth, v.label = 0, 0
	} else if v.bm != 0 {
		// short bitmap is alias of 17 bit bitmap
		v.labelWidth, v.label = 4, v.labelBit-1
	} else {

		size := v.bitTo - v.bitFrom
		if size == 17 {
			v.labelWidth, v.label = 4, v.labelBit-1
		} else if size == 257 {
			v.labelWidth, v.label = 8, v.labelBit-1
		} else {
			panic("unknown bitmap size")
		}
	}
	v.labelEnd = v.prefixEnd + v.labelWidth
}

func (v *scanStackElt) appendLabel(buf *[]byte) {

	labelSize := v.labelWidth
	l := (v.prefixEnd + 7) >> 3
	*buf = (*buf)[:l]

	mask := byte(bitmap.Mask[labelSize])
	if labelSize > 0 {
		if v.prefixEnd&7 != 0 {
			c := (*buf)[l-1]
			(*buf)[l-1] = c&(^mask) | (byte(v.label) & mask)
		} else {
			b := byte(v.label) & mask
			*buf = append(*buf, b<<uint32(8-labelSize))
		}
	}
}

func (v *scanStackElt) appendInnerPrefix(buf *[]byte, qr *querySession) {
	if qr.hasPrefixContent {
		*buf = append((*buf)[:v.prefixStart>>3], qr.prefix[1:]...)
	}
}

func (v *scanStackElt) appendLeafPrefix(buf *[]byte, qr *querySession) {
	*buf = (*buf)[:v.labelEnd>>3]
	if qr.hasLeafPrefix {
		*buf = append(*buf, qr.leafPrefix...)
	}
}

package trie

import (
	"sort"
	"testing"

	"github.com/openacid/slim/encode"
	"github.com/stretchr/testify/require"
)

var defaultScan = []string{
	"",
	"`",
	"a",
	"ab",
	"abc",
	"abca",
	"abcd",
	"abcd1",
	"abce",
	"be",
	"c",
	"cde0",
	"d",
}
var scanCases = map[string]struct {
	keys         []string
	slimStr      string
	paths        [][]int32
	scanFromKeys []string
}{
	"empty": {
		keys:         []string{},
		slimStr:      trim(""),
		paths:        [][]int32{{}},
		scanFromKeys: defaultScan,
	},
	"simple": {
		keys: []string{
			"abc",
			"abcd",
			"abd",
			"abde",
			"bc",
			"bcd",
			"bcde",
			"cde",
		},
		slimStr: trim(`
#000+4*3
    -0001->#001+12*2
               -0011->#004*2
                          -->#008=0
                          -0110->#009=1
               -0100->#005*2
                          -->#010=2
                          -0110->#011=3
    -0010->#002+8*2
               -->#006=4
               -0110->#007+8*2
                          -->#012=5
                          -0110->#013=6
    -0011->#003=7
`),
		paths: [][]int32{
			{0, 1, 4, 8},
			{0, 1, 4, 9},
			{0, 1, 5, 10},
			{0, 1, 5, 11},
			{0, 2, 6},
			{0, 2, 7, 12},
			{0, 2, 7, 13},
			{0, 3},
			{}, // path seeking from after the last key
		},
		scanFromKeys: defaultScan,
	},
	"emptyKey": {
		keys: []string{
			"",
			"a",
			"abc",
			"abd",
			"bc",
			"bcd",
			"cde",
		},
		slimStr: trim(`
#000*2
    -->#001=0
    -0110->#002*3
               -0001->#003*2
                          -->#006=1
                          -0110->#007+12*2
                                     -0011->#010=2
                                     -0100->#011=3
               -0010->#004+8*2
                          -->#008=4
                          -0110->#009=5
               -0011->#005=6
`),
		paths: [][]int32{
			{0, 1},
			{0, 2, 3, 6},
			{0, 2, 3, 7, 10},
			{0, 2, 3, 7, 11},
			{0, 2, 4, 8},
			{0, 2, 4, 9},
			{0, 2, 5},
			{}, // path seeking from after the last key
		},
		scanFromKeys: defaultScan,
	},
}

func TestSlimTrie_Scan(t *testing.T) {

	for name, c := range scanCases {
		t.Run(name, func(t *testing.T) {

			ta := require.New(t)

			values := makeI32s(len(c.keys))

			st, err := NewSlimTrie(encode.I32{}, c.keys, values, Opt{Complete: Bool(true)})
			ta.NoError(err)

			dd(st)
			ta.Equal(c.slimStr, st.String())

			subTestPath(t, st, c.keys, c.paths, c.keys)
			subTestPath(t, st, c.keys, c.paths, c.scanFromKeys)
			subTestScan(t, st, c.keys, c.keys)
			subTestScan(t, st, c.keys, c.scanFromKeys)
			subTestScan(t, st, c.keys, randVStrings(len(c.keys)*5, 0, 10))
		})
	}
}

func TestSlimTrie_Scan_panic(t *testing.T) {

	ta := require.New(t)
	keys := scanCases["simple"].keys
	values := makeI32s(len(keys))

	ta.Panics(func() {
		st, err := NewSlimTrie(encode.I32{}, keys, values, Opt{InnerPrefix: Bool(true)})
		ta.NoError(err)
		st.Scan("abc", true)
	}, "without leaf prefix")

	ta.Panics(func() {
		st, err := NewSlimTrie(encode.I32{}, keys, values)
		ta.NoError(err)
		st.Scan("abc", true)
	}, "without inner prefix")
}

func TestSlimTrie_Scan_slimWithoutValue(t *testing.T) {

	ta := require.New(t)

	c := scanCases["simple"]
	keys := c.keys

	st, err := NewSlimTrie(encode.I32{}, keys, nil, Opt{Complete: Bool(true)})
	ta.NoError(err)

	for _, sk := range c.scanFromKeys {
		idx := sort.SearchStrings(keys, sk)
		nxt := st.Scan(sk, true)

		for i := int32(idx); i < int32(len(keys)); i++ {
			key := keys[i]
			gotKey, gotVal := nxt()
			ta.Equal([]byte(key), gotKey, "scan from: %s %v, idx: %d", sk, []byte(sk), idx)
			ta.Nil(gotVal, "scan from: %s %v, idx: %d", sk, []byte(sk), idx)
		}
		gotKey, gotVal := nxt()
		ta.Nil(gotKey)
		ta.Nil(gotVal)
	}
}

func TestSlimTrie_Scan_large(t *testing.T) {

	testBigKeySet(t, func(t *testing.T, keys []string) {
		ta := require.New(t)

		values := makeI32s(len(keys))

		st, err := NewSlimTrie(encode.I32{}, keys, values, Opt{Complete: Bool(true)})
		ta.NoError(err)

		subTestScan(t, st, keys, randVStrings(clap(len(keys), 50, 10*1024), 0, 10))
	})
}

var OutputScan int

func BenchmarkSlimTrie_Scan(b *testing.B) {

	typ := "1mvl5_10"

	keys := getKeys(typ)
	n := len(keys)
	values := makeI32s(n)

	st, err := NewSlimTrie(encode.I32{}, keys, values, Opt{Complete: Bool(true)})
	if err != nil {
		panic("err:" + err.Error())
	}

	scanN := 1024 * 100

	b.ResetTimer()

	s := 0

	for i := 0; i < b.N/scanN; i++ {
		nxt := st.Scan("`", true)
		for j := 0; j < scanN; j++ {
			b, _ := nxt()
			s += int(b[0])

		}
	}
	OutputScan = s
}

func subTestPath(
	t *testing.T,
	st *SlimTrie,
	keys []string,
	paths [][]int32,
	scanFromKeys []string,
) {

	t.Run("getGEPath", func(t *testing.T) {

		ta := require.New(t)

		// searching from other keys should start from next present key.
		for _, sk := range scanFromKeys {
			idx := sort.SearchStrings(keys, sk)
			p := st.getGEPath(sk)
			ta.Equal(paths[idx], p, "key: %s %v", sk, []byte(sk))
		}
	})

}
func subTestScan(
	t *testing.T,
	st *SlimTrie,
	keys []string,
	scanFromKeys []string,
) {
	t.Run("Scan", func(t *testing.T) {

		ta := require.New(t)

		for _, sk := range scanFromKeys {
			idx := sort.SearchStrings(keys, sk)
			nxt := st.Scan(sk, true)

			var i int32
			for i = int32(idx); i < int32(len(keys)) && i < int32(idx+200); i++ {
				key := keys[i]
				gotKey, gotVal := nxt()
				ta.Equal([]byte(key), gotKey, "scan from: %s %v, idx: %d", sk, []byte(sk), idx)
				ta.Equal(st.encoder.Encode(i), gotVal, "scan from: %s %v, idx: %d", sk, []byte(sk), idx)
			}
			if i == int32(len(keys)) {
				ta.Nil(nxt())
			}

			{ // scan without yielding value
				nxt := st.Scan(sk, false)
				_, gotVal := nxt()
				ta.Nil(gotVal)
				_, gotVal = nxt()
				ta.Nil(gotVal)
			}
		}
	})
}

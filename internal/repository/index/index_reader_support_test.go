package index

import (
	"fmt"
	"strings"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

type diagnosticIndexOutcome struct {
	err   string
	panic string
	packs restic.IDs
	ids   restic.IDs
	final bool
	blobs map[pack.PackedBlob]int
}

func diagnosticDecodeOutcome(decode func([]byte, restic.ID) (*Index, error), buf []byte) (out diagnosticIndexOutcome) {
	defer func() {
		if p := recover(); p != nil {
			out.panic = fmt.Sprint(p)
		}
	}()
	idx, err := decode(buf, restic.Hash([]byte("index identity")))
	if err != nil {
		out.err = err.Error()
		if idx != nil {
			out.err += "; returned partial index"
		}
		return out
	}
	out.packs, out.ids, out.final = idx.packs, idx.ids, idx.final
	out.blobs = make(map[pack.PackedBlob]int)
	for typ := range idx.byType {
		for e := range idx.byType[typ].values() {
			out.blobs[*idx.toPackedBlob(e, restic.BlobType(typ))]++
		}
	}
	return out
}

func diagnosticDecodeCases() []string {
	id := `"` + strings.Repeat("1", 64) + `"`
	blob := `{"id":` + id + `,"type":"data","offset":7,"length":11,"uncompressed_length":19}`
	p := `{"id":` + id + `,"blobs":[` + blob + `]}`
	overflow := `{"blobs":[{"offset":4294967296}]}`
	return []string{
		`{"packs":[` + p + `,{}]}`,
		`{"packs":[` + p + `,{"blobs":[{}]}]}`,
		`{"packs":[` + p + `,null,{"blobs":[null]}]}`,
		`{"packs":[` + p + `,{"blobs":null}]}`,
		`{"packs":[{"blobs":[` + blob + `],"blobs":[{}]}]}`,
		`{"packs":[{"id":` + id + `,"ID":` + id + `,"blobs":[{"type":"tree","type":"data"}]}]}`,
		`{"packs":[{"ignored":{"array":[1,true,null,"}\\\"[]"]},"blobs":[{}]}]}`,
		`{"packs":[]}`,
		`{"packs":null}`,
		`{}`,
		`null`,
		`[]`,
		`{"packs":[` + p + `],"packs":[{}]}`,
		`{"packs":[` + p + `],"PACKS":[{"blobs":[{}]}]}`,
		`{"pac\u006bs":[` + p + `]}`,
		`{"pacKs":[` + p + `]}`,
		`{"supersedes":["ignored"],"packs":[` + p + `]}`,
		`{"packs":[` + p + `],"unknown":{"nested":[1]}}`,
		`{"packs":[{"id":"invalid","blobs":[{}]}]}`,
		`{"packs":[{"blobs":[{"type":"invalid"}]}]}`,
		`{"packs":[{"blobs":[{"type":null}]}]}`,
		`{"packs":[{"blobs":[{"id":null}]}]}`,
		`{"packs":[{"blobs":[{"length":-1}]}]}`,
		`{"packs":[{"blobs":[{"length":1.5}]}]}`,
		`{"packs":[{"blobs":[{"offset":"7"}]}]}`,
		`{"packs":[{"blobs":[{"offset":18446744073709551616}]}]}`,
		`{"packs":[` + overflow + `]}`,
		`{"packs":[` + overflow + `,{"id":"bad"}]}`,
		`{"packs":[` + overflow + `,{"blobs":[{"length":-1}]}]}`,
		`{"packs":[` + overflow + `,{"blobs":[{"type":"bad"}]}]}`,
		`{"packs":[` + overflow + `,{"blobs":42}]}`,
		`{"packs":[` + overflow + `,`,
		`{"packs":[` + p + `]} true`,
	}
}

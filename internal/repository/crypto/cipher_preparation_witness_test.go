package crypto

// WITNESS for commit 0dc6aea "A5" (cipher_preparation.go, crypto.go,
// open_reader.go, repository/key.go). Copy to internal/repository/crypto/ of
// the final tree and run
//
//	go test -run TestWitnessFinalA5 -count=1 -p 2 -v ./internal/repository/crypto
//
// The reference below is stock's code (trees/stock, crypto.go:50-87 and
// 243-323), copied verbatim: its own poly1305 helpers, its own Seal and Open
// with every parameter check, panic and error. Nothing of the tree under
// test is used by the reference except the Key's key bytes.
//
// What it shows.
//
//  1. TestWitnessFinalA5Differential: a population of Key values that share
//     prepared pairs in every way the type allows (a key and by-value copies
//     of it made before and after the first use, a key from NewRandomKey, a
//     key that was never prepared, a prepared zero key) goes through 60,000
//     random steps: Seal, Open, OpenReader (also in place, as the repository
//     calls them), a changed byte of the encryption key, of the MAC key K or
//     of R, one key's bytes copied over another's, a by-value copy over
//     another Key, a JSON round trip into a prepared key, damaged input, a
//     ciphertext of one key offered to another key that shares its pair. At
//     every step the result (bytes, error, panic value) must be stock's for
//     the key bytes the Key holds at that step.
//
//  2. TestWitnessFinalA5SharedPairConcurrent: Key values with DIFFERENT
//     bytes share one prepared pair and are used by goroutines at the same
//     time (the worst case of "a copied Key that was changed"). Every
//     ciphertext must be stock's for the goroutine's own key, and no key may
//     open another key's ciphertext.
//
//  3. TestWitnessFinalA5KnownAnswers: the cr.yp.to Poly1305-AES vectors and
//     restic's own vector through the functions production now calls
//     (preparedCipherPair.sum / verify, Seal, Open) with a prepared and an
//     unprepared key. Stock's TestPoly1305 still tests poly1305MAC and
//     poly1305Verify, which production no longer calls.
//
//  4. TestWitnessFinalA5InvalidKeyAndJSON: a prepared zero key panics in
//     Seal and fails in Open and OpenReader as stock does and keeps no pair;
//     the JSON of a prepared key is stock's JSON.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"testing"

	"github.com/restic/restic/internal/errors"

	"golang.org/x/crypto/poly1305"
)

// ---- stock, verbatim (trees/stock/internal/repository/crypto/crypto.go) ----

func a5StockPoly1305MAC(msg []byte, nonce []byte, key *MACKey) []byte {
	k := a5StockPoly1305PrepareKey(nonce, key)

	var out [16]byte
	poly1305.Sum(&out, msg, &k)

	return out[:]
}

// prepare key for low-level poly1305.Sum(): r||n
func a5StockPoly1305PrepareKey(nonce []byte, key *MACKey) [32]byte {
	var k [32]byte

	cipher, err := aes.NewCipher(key.K[:])
	if err != nil {
		panic(err)
	}
	cipher.Encrypt(k[16:], nonce[:])

	copy(k[:16], key.R[:])

	return k
}

func a5StockPoly1305Verify(msg []byte, nonce []byte, key *MACKey, mac []byte) bool {
	k := a5StockPoly1305PrepareKey(nonce, key)

	var m [16]byte
	copy(m[:], mac)

	return poly1305.Verify(&m, msg, &k)
}

func a5StockSeal(k *Key, dst, nonce, plaintext, additionalData []byte) []byte {
	if !k.Valid() {
		panic("key is invalid")
	}

	if len(additionalData) > 0 {
		panic("additional data is not supported")
	}

	if len(nonce) != ivSize {
		panic("incorrect nonce length")
	}

	if !validNonce(nonce) {
		panic("nonce is invalid")
	}

	ret, out := sliceForAppend(dst, len(plaintext)+k.Overhead())

	c, err := aes.NewCipher(k.EncryptionKey[:])
	if err != nil {
		panic(fmt.Sprintf("unable to create cipher: %v", err))
	}
	e := cipher.NewCTR(c, nonce)
	e.XORKeyStream(out, plaintext)

	mac := a5StockPoly1305MAC(out[:len(plaintext)], nonce, &k.MACKey)
	copy(out[len(plaintext):], mac)

	return ret
}

func a5StockOpen(k *Key, dst, nonce, ciphertext, _ []byte) ([]byte, error) {
	if !k.Valid() {
		return nil, errors.New("invalid key")
	}

	// check parameters
	if len(nonce) != ivSize {
		panic("incorrect nonce length")
	}

	if !validNonce(nonce) {
		return nil, errors.New("nonce is invalid")
	}

	// check for plausible length
	if len(ciphertext) < k.Overhead() {
		return nil, errors.Errorf("trying to decrypt invalid data: ciphertext too short")
	}

	l := len(ciphertext) - macSize
	ct, mac := ciphertext[:l], ciphertext[l:]

	// verify mac
	if !a5StockPoly1305Verify(ct, nonce, &k.MACKey, mac) {
		return nil, ErrUnauthenticated
	}

	ret, out := sliceForAppend(dst, len(ct))

	c, err := aes.NewCipher(k.EncryptionKey[:])
	if err != nil {
		panic(fmt.Sprintf("unable to create cipher: %v", err))
	}
	e := cipher.NewCTR(c, nonce)
	e.XORKeyStream(out, ct)

	return ret, nil
}

// ---- comparison helpers ----

type a5Result struct {
	out      []byte
	err      string
	panicked string
}

func a5Capture(f func() ([]byte, error)) (r a5Result) {
	defer func() {
		if p := recover(); p != nil {
			r = a5Result{panicked: fmt.Sprint(p)}
		}
	}()
	out, err := f()
	r.out = bytes.Clone(out)
	if out == nil {
		r.out = nil
	}
	if err != nil {
		r.err = err.Error()
	}
	return r
}

func (r a5Result) equal(o a5Result) bool {
	return bytes.Equal(r.out, o.out) && (r.out == nil) == (o.out == nil) && r.err == o.err && r.panicked == o.panicked
}

func (r a5Result) String() string {
	return fmt.Sprintf("{%d bytes, nil=%v, err=%q, panic=%q}", len(r.out), r.out == nil, r.err, r.panicked)
}

func a5Nonce(rnd *rand.Rand) []byte {
	nonce := make([]byte, ivSize)
	switch rnd.Intn(40) {
	case 0:
		return nonce // all zero: invalid
	case 1:
		return nonce[:rnd.Intn(ivSize)] // wrong length
	}
	for !validNonce(nonce) {
		rnd.Read(nonce)
	}
	return nonce
}

var a5Sizes = []int{0, 1, 15, 16, 17, 31, 32, 33, 100, 255, 256, 1000, 4095, 4096, 4097, 20000, 70000}

// a5Step runs one random operation on k and compares it with stock.
func a5Step(t testing.TB, rnd *rand.Rand, name string, k *Key, others []*Key) {
	t.Helper()
	// stock sees only the key bytes: a Key without a holder
	ref := &Key{MACKey: k.MACKey, EncryptionKey: k.EncryptionKey}
	nonce := a5Nonce(rnd)
	plaintext := make([]byte, a5Sizes[rnd.Intn(len(a5Sizes))])
	rnd.Read(plaintext)

	check := func(what string, got, want a5Result) {
		t.Helper()
		if !got.equal(want) {
			t.Fatalf("%s, %s (nonce %d bytes, %d bytes of plaintext): got %v, stock %v", name, what, len(nonce), len(plaintext), got, want)
		}
	}

	// Seal: appended to nil, appended to a prefix, and in place
	prefix := make([]byte, rnd.Intn(3)*7)
	rnd.Read(prefix)
	want := a5Capture(func() ([]byte, error) { return a5StockSeal(ref, bytes.Clone(prefix), nonce, plaintext, nil), nil })
	check("Seal", a5Capture(func() ([]byte, error) { return k.Seal(bytes.Clone(prefix), nonce, plaintext, nil), nil }), want)
	inPlace := func(seal func(dst, nonce, pt, ad []byte) []byte) a5Result {
		buf := make([]byte, len(plaintext), len(plaintext)+macSize)
		copy(buf, plaintext)
		return a5Capture(func() ([]byte, error) { return seal(buf[:0], nonce, buf, nil), nil })
	}
	check("Seal in place",
		inPlace(k.Seal),
		inPlace(func(dst, nonce, pt, ad []byte) []byte { return a5StockSeal(ref, dst, nonce, pt, ad) }))

	if want.panicked != "" {
		// invalid nonce or key: Open and OpenReader on arbitrary bytes
		junk := make([]byte, rnd.Intn(64))
		rnd.Read(junk)
		wantOpen := a5Capture(func() ([]byte, error) { return a5StockOpen(ref, nil, nonce, junk, nil) })
		check("Open of junk", a5Capture(func() ([]byte, error) { return k.Open(nil, nonce, junk, nil) }), wantOpen)
		gotRd := a5Capture(func() ([]byte, error) {
			rd, err := k.OpenReader(nonce, junk)
			if err != nil {
				return nil, err
			}
			return io.ReadAll(rd)
		})
		if gotRd.err != wantOpen.err || gotRd.panicked != wantOpen.panicked {
			t.Fatalf("%s, OpenReader of junk: got %v, stock's Open %v", name, gotRd, wantOpen)
		}
		return
	}
	sealed := want.out[len(prefix):]

	// Open: the valid ciphertext, a damaged one, a short one
	inputs := [][]byte{sealed}
	damaged := bytes.Clone(sealed)
	damaged[rnd.Intn(len(damaged))] ^= 1 << uint(rnd.Intn(8))
	inputs = append(inputs, damaged, sealed[:rnd.Intn(len(sealed))])
	for i, in := range inputs {
		for _, n := range [][]byte{nonce, func() []byte {
			b := bytes.Clone(nonce)
			b[rnd.Intn(len(b))] ^= 1 << uint(rnd.Intn(8))
			return b
		}()} {
			wantOpen := a5Capture(func() ([]byte, error) { return a5StockOpen(ref, nil, n, bytes.Clone(in), nil) })
			check(fmt.Sprintf("Open of input %d", i), a5Capture(func() ([]byte, error) { return k.Open(nil, n, bytes.Clone(in), nil) }), wantOpen)
			// as the repository calls it: plaintext over the ciphertext
			wantIn := a5Capture(func() ([]byte, error) { b := bytes.Clone(in); return a5StockOpen(ref, b[:0], n, b, nil) })
			check(fmt.Sprintf("Open in place of input %d", i), a5Capture(func() ([]byte, error) { b := bytes.Clone(in); return k.Open(b[:0], n, b, nil) }), wantIn)
			gotRd := a5Capture(func() ([]byte, error) {
				rd, err := k.OpenReader(n, bytes.Clone(in))
				if err != nil {
					return nil, err
				}
				return io.ReadAll(rd)
			})
			if gotRd.err != wantOpen.err || gotRd.panicked != wantOpen.panicked || (wantOpen.err == "" && wantOpen.panicked == "" && !bytes.Equal(gotRd.out, wantOpen.out)) {
				t.Fatalf("%s, OpenReader of input %d: got %v, stock's Open %v", name, i, gotRd, wantOpen)
			}
		}
	}

	// Another key must treat this ciphertext exactly as stock does, whatever
	// pair it shares with k. (Stock's verdict, not "rejected": Poly1305-AES
	// of an empty message does not depend on R, and R is clamped, so two
	// different MAC keys can be equivalent in stock too.)
	for _, o := range others {
		oref := &Key{MACKey: o.MACKey, EncryptionKey: o.EncryptionKey}
		wantOther := a5Capture(func() ([]byte, error) { return a5StockOpen(oref, nil, nonce, bytes.Clone(sealed), nil) })
		gotOther := a5Capture(func() ([]byte, error) { return o.Open(nil, nonce, bytes.Clone(sealed), nil) })
		if !gotOther.equal(wantOther) {
			t.Fatalf("%s: a ciphertext of this key offered to another key: got %v, stock %v", name, gotOther, wantOther)
		}
	}
}

func TestWitnessFinalA5Differential(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261007))

	base := NewRandomKey() // prepared at construction
	early := &Key{MACKey: base.MACKey, EncryptionKey: base.EncryptionKey}
	early.Prepare()
	earlyCopy := *early // copied before the first use: shares the holder
	// first use
	a5Step(t, rnd, "early", early, nil)
	lateCopy := *early // copied after the first use: shares holder and pair
	plain := &Key{MACKey: base.MACKey, EncryptionKey: base.EncryptionKey}
	zero := &Key{}
	zero.Prepare()
	second := NewRandomKey()
	secondCopy := *second

	names := []string{"base (NewRandomKey)", "early (Prepare)", "copy before first use", "copy after first use", "never prepared", "prepared zero key", "second (NewRandomKey)", "copy of second"}
	keys := []*Key{base, early, &earlyCopy, &lateCopy, plain, zero, second, &secondCopy}

	var steps, changes, copies, jsonTrips int
	for i := 0; i < 60000; i++ {
		ki := rnd.Intn(len(keys))
		k := keys[ki]
		switch rnd.Intn(14) {
		case 0:
			k.EncryptionKey[rnd.Intn(len(k.EncryptionKey))] ^= byte(1 + rnd.Intn(255))
			changes++
		case 1:
			k.MACKey.K[rnd.Intn(len(k.MACKey.K))] ^= byte(1 + rnd.Intn(255))
			changes++
		case 2:
			k.MACKey.R[rnd.Intn(len(k.MACKey.R))] ^= byte(1 + rnd.Intn(255))
			changes++
		case 3:
			// the bytes of another key, the holder stays
			o := keys[rnd.Intn(len(keys))]
			k.MACKey, k.EncryptionKey = o.MACKey, o.EncryptionKey
			changes++
		case 4:
			// a by-value copy over this Key: bytes and holder of the other
			*k = *keys[rnd.Intn(len(keys))]
			copies++
		case 5:
			// JSON round trip of another key into this (possibly prepared) Key
			buf, err := json.Marshal(keys[rnd.Intn(len(keys))])
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(buf, k); err != nil {
				t.Fatal(err)
			}
			jsonTrips++
		case 6:
			// swap the two halves of the key material that have the same
			// size: K and R of the MAC key
			k.MACKey.K, k.MACKey.R = k.MACKey.R, k.MACKey.K
			changes++
		}
		var others []*Key
		for j, o := range keys {
			if j != ki && rnd.Intn(3) == 0 {
				others = append(others, o)
			}
		}
		a5Step(t, rnd, fmt.Sprintf("step %d, key %q", i, names[ki]), k, others)
		steps++
	}
	t.Logf("%d steps on 8 Key values: %d changes of key bytes, %d by-value copies, %d JSON round trips; every Seal, Open and OpenReader result equal to stock's for the key bytes of that step", steps, changes, copies, jsonTrips)
}

func TestWitnessFinalA5SharedPairConcurrent(t *testing.T) {
	// One holder, shared by Key values with different bytes.
	root := NewRandomKey()
	const goroutines = 12
	keys := make([]Key, goroutines)
	for i := range keys {
		keys[i] = *root // by-value copy: same holder
		if i%3 != 0 {
			// two of three get their own key bytes
			keys[i].EncryptionKey[i%32] ^= byte(i + 1)
			keys[i].MACKey.K[i%16] ^= byte(i + 1)
			keys[i].MACKey.R[(i+5)%16] ^= byte(i + 1)
		}
		if keys[i].preparation != root.preparation || keys[i].preparation == nil {
			t.Fatal("the copies do not share the holder: the witness tests nothing")
		}
	}
	type sealedMsg struct {
		from             int
		nonce, ct, plain []byte
	}
	exchange := make(chan sealedMsg, 4096)
	var wg sync.WaitGroup
	failures := make(chan string, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := &keys[g] // only this goroutine touches this Key value
			ref := &Key{MACKey: k.MACKey, EncryptionKey: k.EncryptionKey}
			rnd := rand.New(rand.NewSource(int64(g) + 1))
			fail := func(format string, args ...any) {
				select {
				case failures <- fmt.Sprintf("goroutine %d: ", g) + fmt.Sprintf(format, args...):
				default:
				}
			}
			for i := 0; i < 4000; i++ {
				nonce := make([]byte, ivSize)
				for !validNonce(nonce) {
					rnd.Read(nonce)
				}
				plaintext := make([]byte, rnd.Intn(600))
				rnd.Read(plaintext)
				sealed := k.Seal(nil, nonce, plaintext, nil)
				if !bytes.Equal(sealed, a5StockSeal(ref, nil, nonce, plaintext, nil)) {
					fail("Seal is not stock's ciphertext for this goroutine's key")
					return
				}
				opened, err := k.Open(nil, nonce, sealed, nil)
				if err != nil || !bytes.Equal(opened, plaintext) {
					fail("Open of the own ciphertext: %v", err)
					return
				}
				select {
				case exchange <- sealedMsg{from: g, nonce: nonce, ct: sealed, plain: plaintext}:
				default:
				}
				select {
				case m := <-exchange:
					got, err := k.Open(nil, m.nonce, bytes.Clone(m.ct), nil)
					want, werr := a5StockOpen(ref, nil, m.nonce, bytes.Clone(m.ct), nil)
					if (err == nil) != (werr == nil) || !bytes.Equal(got, want) {
						fail("a ciphertext of goroutine %d: got (%d bytes, %v), stock (%d bytes, %v)", m.from, len(got), err, len(want), werr)
						return
					}
					if keys[m.from].MACKey != k.MACKey && err == nil {
						fail("the key of goroutine %d (another MAC key) was authenticated here", m.from)
						return
					}
				default:
				}
			}
		}(g)
	}
	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Error(msg)
	}
}

func TestWitnessFinalA5KnownAnswers(t *testing.T) {
	// cr.yp.to Poly1305-AES vectors through the functions production calls
	for i, test := range poly1305Tests {
		for _, prepared := range []bool{false, true} {
			k := &Key{}
			copy(k.MACKey.K[:], test.k)
			copy(k.MACKey.R[:], test.r)
			// any valid encryption key: the MAC must not depend on it
			for j := range k.EncryptionKey {
				k.EncryptionKey[j] = byte(j + i + 1)
			}
			if prepared {
				k.Prepare()
			}
			for round := 0; round < 2; round++ { // second round: the kept pair
				p := k.preparedCiphers()
				if mac := p.sum(test.msg, test.nonce); !bytes.Equal(mac, test.mac) {
					t.Fatalf("vector %d (prepared %v, round %d): mac %02x, want %02x", i, prepared, round, mac, test.mac)
				}
				if !p.verify(test.msg, test.nonce, test.mac) {
					t.Fatalf("vector %d (prepared %v): the vector's mac does not verify", i, prepared)
				}
				bad := bytes.Clone(test.mac)
				bad[3] ^= 4
				if p.verify(test.msg, test.nonce, bad) {
					t.Fatalf("vector %d (prepared %v): a wrong mac verifies", i, prepared)
				}
			}
		}
	}
	// restic's own vector through Open and Seal
	for _, tv := range testValues {
		for _, prepared := range []bool{false, true} {
			k := &Key{EncryptionKey: tv.ekey, MACKey: tv.skey}
			if prepared {
				k.Prepare()
			}
			for round := 0; round < 2; round++ {
				nonce, ciphertext := tv.ciphertext[:16], tv.ciphertext[16:]
				p, err := k.Open(nil, nonce, ciphertext, nil)
				if err != nil || !bytes.Equal(p, tv.plaintext) {
					t.Fatalf("restic vector (prepared %v, round %d): %q, %v", prepared, round, p, err)
				}
				if sealed := k.Seal(nil, nonce, tv.plaintext, nil); !bytes.Equal(sealed, ciphertext) {
					t.Fatalf("restic vector (prepared %v, round %d): Seal gives %02x, the vector is %02x", prepared, round, sealed, ciphertext)
				}
			}
		}
	}
}

func TestWitnessFinalA5InvalidKeyAndJSON(t *testing.T) {
	nonce := make([]byte, ivSize)
	nonce[0] = 1
	for _, k := range []*Key{{}, func() *Key { k := &Key{}; k.Prepare(); return k }()} {
		got := a5Capture(func() ([]byte, error) { return k.Seal(nil, nonce, []byte("x"), nil), nil })
		if got.panicked != "key is invalid" {
			t.Fatalf("Seal with the zero key: %v", got)
		}
		if _, err := k.Open(nil, nonce, make([]byte, 40), nil); err == nil || err.Error() != "invalid key" {
			t.Fatalf("Open with the zero key: %v", err)
		}
		if _, err := k.OpenReader(nonce, make([]byte, 40)); err == nil || err.Error() != "invalid key" {
			t.Fatalf("OpenReader with the zero key: %v", err)
		}
		if k.preparation != nil && k.preparation.current.Load() != nil {
			t.Fatal("the zero key kept a pair")
		}
	}

	// half-valid keys: stock refuses them before any cipher is built
	half := NewRandomKey()
	half.MACKey.R = [16]byte{}
	if got := a5Capture(func() ([]byte, error) { return half.Seal(nil, nonce, []byte("x"), nil), nil }); got.panicked != "key is invalid" {
		t.Fatalf("Seal with R = 0: %v", got)
	}

	prepared := NewRandomKey()
	plain := &Key{MACKey: prepared.MACKey, EncryptionKey: prepared.EncryptionKey}
	_ = prepared.Seal(nil, nonce, []byte("first use"), nil)
	a, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("JSON of a prepared key differs from JSON of the same key bytes:\n%s\n%s", a, b)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(a, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["mac"] == nil || fields["encrypt"] == nil {
		t.Fatalf("JSON of a key has other fields than stock's mac and encrypt: %s", a)
	}
	// the master key travels as JSON into a new Key (AddKey, openKey): the new
	// Key has no holder until Prepare is called and must behave as stock
	var decoded Key
	if err := json.Unmarshal(a, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.preparation != nil {
		t.Fatal("a decoded key has a holder")
	}
	if !bytes.Equal(decoded.Seal(nil, nonce, []byte("abc"), nil), prepared.Seal(nil, nonce, []byte("abc"), nil)) {
		t.Fatal("a decoded key seals differently")
	}
}

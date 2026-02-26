package sstable
import "fmt"
import "testing"

func buildTestTable(t *testing.T, entries []struct{
	key string
	val string
	tomb bool
}) string {

	dir := t.TempDir()
	file := dir + "/test.sst"

	b, err := NewBuilder(file,len(entries))
	if err != nil{
		t.Fatal(err)
	}

	for _,e := range entries{
		err := b.Add([]byte(e.key),[]byte(e.val),e.tomb)
		if err!=nil{
			t.Fatal(err)
		}
	}

	err=b.Close()
	if err!=nil{
		t.Fatal(err)
	}

	return file
}

func TestCreateSSTableFile(t *testing.T){

	file := buildTestTable(t, []struct{
		key string
		val string
		tomb bool
	}{
		{"apple","1",false},
		{"banana","2",false},
	})

	if file==""{
		t.Fatal("file not created")
	}
}
func TestBuildAndGet(t *testing.T){

    file := buildTestTable(t, []struct{
        key string
        val string
        tomb bool
    }{
        {"apple","1",false},
        {"banana","2",false},
    })

    r, err := OpenSSTable(file)
    if err != nil {
        t.Fatal(err)
    }
    defer r.Close()

    val, tomb, found, err := r.Get("apple")

    if err != nil {
        t.Fatal(err)
    }

    if !found || tomb || val!="1" {
        t.Fatal("expected apple=1")
    }
}
func TestTombstoneHandling(t *testing.T){

	file := buildTestTable(t, []struct{
		key string
		val string
		tomb bool
	}{
		{"apple","1",false},
		{"banana","",true},   // tombstone
	})

	r, err := OpenSSTable(file)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	val, tomb, found, err := r.Get("banana")

	if err != nil {
		t.Fatal(err)
	}

	if !found || !tomb {
		t.Fatal("banana should be marked as tombstone")
	}

	if val != "" {
		t.Fatal("tombstone should not return value")
	}
}
func TestBloomBlocksMissingKey(t *testing.T){

	file := buildTestTable(t, []struct{
		key string
		val string
		tomb bool
	}{
		{"apple","1",false},
		{"banana","2",false},
	})

	r, err := OpenSSTable(file)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	_, tomb, found, err := r.Get("not_present")

	if err != nil {
		t.Fatal(err)
	}

	if found || tomb {
		t.Fatal("missing key should not be found")
	}
}
func TestSparseIndexLookup(t *testing.T){

	var entries []struct{
		key string
		val string
		tomb bool
	}

	// create MANY keys to force multiple blocks
	for i:=0;i<200;i++{
		entries = append(entries, struct{
			key string
			val string
			tomb bool
		}{
			key:  fmt.Sprintf("key%03d",i),
			val:  fmt.Sprintf("val%03d",i),
			tomb: false,
		})
	}

	file := buildTestTable(t, entries)

	r, err := OpenSSTable(file)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	// test first
	v, _, found, _ := r.Get("key000")
	if !found || v!="val000" {
		t.Fatal("failed first key lookup")
	}

	// test middle
	v, _, found, _ = r.Get("key100")
	if !found || v!="val100" {
		t.Fatal("failed middle key lookup")
	}

	// test last
	v, _, found, _ = r.Get("key199")
	if !found || v!="val199" {
		t.Fatal("failed last key lookup")
	}
}
func TestIteratorSortedOrder(t *testing.T){

	file := buildTestTable(t, []struct{
		key string
		val string
		tomb bool
	}{
		{"dog","3",false},
		{"apple","1",false},
		{"cat","2",false},
	})

	it, err := NewIterator(file)
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()

	var keys []string

	for it.Valid {
		keys = append(keys, it.Key)
		it.Next()
	}

	expected := []string{"dog","apple","cat"} // IMPORTANT
	if len(keys) != len(expected) {
		t.Fatal("iterator returned wrong number of keys")
	}

	for i := range expected {
		if keys[i] != expected[i] {
			t.Fatal("iterator order mismatch")
		}
	}
}
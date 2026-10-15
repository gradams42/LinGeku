package devicedb

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadRealDatabase(t *testing.T) {
	share := FindShareDir("../../third_party/minipro")
	if share == "" {
		t.Skip("minipro database not found; install minipro or set MINIPRO_SHARE")
	}
	infoic, logic := filepath.Join(share, "infoic.xml"), filepath.Join(share, "logicic.xml")
	start := time.Now()
	db, err := Load(infoic, logic)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded in %v", time.Since(start))
	for _, p := range Programmers {
		t.Logf("%s: %d devices", p.Label(), len(db.Devices(p)))
	}
	d := db.Find(T48, "AT28C256")
	if d == nil {
		t.Fatal("AT28C256 not found")
	}
	if d.CodeBytes() != 32768 {
		t.Errorf("AT28C256 size = %d", d.CodeBytes())
	}
	m := db.Find(TL866II, "ATMEGA328P@DIP28")
	if m == nil || db.Config(m) == nil || len(db.Config(m).Fuses) == 0 {
		t.Fatalf("ATMEGA328P config missing: %+v", m)
	}
	t.Logf("%s id=%s pkg=%s cfg=%+v", m.Name, m.ChipIDString(), m.Package(), db.Config(m))
	if len(db.Logic) == 0 || db.Find(T48, "7404") == nil {
		t.Fatal("logic ICs missing")
	}
}

package mail

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
)

// The Outlook client ID is sealed in settings (microsoftClientSetting): the key rotation re-seals the fields listed for
// it in db.SealedSettings, so the sealed field of sealedSetting must be there.
func TestTheMicrosoftClientIsReSealed(t *testing.T) {
	i := slices.IndexFunc(db.SealedSettings(), func(s db.SealedSetting) bool { return s.Key == microsoftClientSetting })
	if i < 0 || db.SealedSettings()[i].List {
		t.Fatalf("%s is not an object in db.SealedSettings", microsoftClientSetting)
	}
	typ := reflect.TypeOf(sealedSetting{})
	for i2 := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i2).Tag.Get("json"), ",")
		if !slices.Contains(db.SealedSettings()[i].Fields, name) {
			t.Errorf("sealedSetting.%s is not re-sealed by the key rotation", name)
		}
	}
}

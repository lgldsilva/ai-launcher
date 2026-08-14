package launcher

import (
	"reflect"
	"testing"
)

// upsertEnv must not mutate the backing array of the slice it receives,
// because callers may still hold the original environment.
func TestUpsertEnvDoesNotMutateOriginalSlice(t *testing.T) {
	original := []string{"PATH=/usr/bin", "AI_MEMORY_AUTH_TOKEN=old", "HOME=/home/tester"}
	wantOriginal := []string{"PATH=/usr/bin", "AI_MEMORY_AUTH_TOKEN=old", "HOME=/home/tester"}

	got := upsertEnv(original, "AI_MEMORY_AUTH_TOKEN", "new")
	if !reflect.DeepEqual(original, wantOriginal) {
		t.Fatalf("original env mutated = %#v; want %#v", original, wantOriginal)
	}

	want := []string{"PATH=/usr/bin", "HOME=/home/tester", "AI_MEMORY_AUTH_TOKEN=new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upsertEnv() = %#v; want %#v", got, want)
	}
}

func TestUpsertEnvRemovesKeyWhenValueEmpty(t *testing.T) {
	env := []string{"PATH=/usr/bin", "AI_MEMORY_SERVER_URL=http://old", "HOME=/home/tester"}
	got := upsertEnv(env, "AI_MEMORY_SERVER_URL", "")
	for _, entry := range got {
		if entry == "AI_MEMORY_SERVER_URL=http://old" {
			t.Fatalf("upsertEnv() kept the old value: %#v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("upsertEnv() = %#v; want 2 entries", got)
	}
}

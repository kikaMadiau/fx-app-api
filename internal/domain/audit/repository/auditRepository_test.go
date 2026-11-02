package repository

import (
	"encoding/json"
	"testing"
)

func TestMarshalNullable(t *testing.T) {
	got, err := marshalNullable(map[string]string{"state": "VERIFIED"})
	if err != nil {
		t.Fatal(err)
	}
	serialized, ok := got.(string)
	if !ok {
		t.Fatalf("audit JSON parameter type is %T, want string", got)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(serialized), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["state"] != "VERIFIED" {
		t.Fatalf("decoded audit data = %#v", decoded)
	}
	if got, err := marshalNullable(nil); err != nil || got != nil {
		t.Fatalf("nil audit state = %v, %v", got, err)
	}
}

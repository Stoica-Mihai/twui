package session

import "testing"

func TestOptions_GetMissing_ReturnsNil(t *testing.T) {
	o := NewOptions()
	if got := o.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}
}

func TestOptions_SetDefault_UsedWhenNoOverride(t *testing.T) {
	o := NewOptions()
	o.SetDefault("color", "blue")

	if got := o.Get("color"); got != "blue" {
		t.Errorf("Get = %v, want %q", got, "blue")
	}
}

func TestOptions_Set_OverridesDefault(t *testing.T) {
	o := NewOptions()
	o.SetDefault("size", 10)
	o.Set("size", 20)

	if got := o.Get("size"); got != 20 {
		t.Errorf("Get = %v, want 20", got)
	}
}

func TestOptions_GetString(t *testing.T) {
	o := NewOptions()
	o.Set("name", "alice")

	if got := o.GetString("name"); got != "alice" {
		t.Errorf("GetString = %q, want %q", got, "alice")
	}
}

func TestOptions_GetString_NonString_ReturnsEmpty(t *testing.T) {
	o := NewOptions()
	o.Set("num", 42)

	if got := o.GetString("num"); got != "" {
		t.Errorf("GetString on non-string = %q, want empty", got)
	}
}

func TestOptions_GetInt(t *testing.T) {
	o := NewOptions()
	o.Set("count", 7)

	if got := o.GetInt("count"); got != 7 {
		t.Errorf("GetInt = %d, want 7", got)
	}
}

func TestOptions_GetInt_FromFloat64(t *testing.T) {
	o := NewOptions()
	o.Set("n", float64(5))

	if got := o.GetInt("n"); got != 5 {
		t.Errorf("GetInt from float64 = %d, want 5", got)
	}
}

func TestOptions_GetBool(t *testing.T) {
	o := NewOptions()
	o.Set("enabled", true)

	if got := o.GetBool("enabled"); !got {
		t.Error("GetBool = false, want true")
	}
}

func TestOptions_GetBool_NonBool_ReturnsFalse(t *testing.T) {
	o := NewOptions()
	o.Set("x", "yes")

	if got := o.GetBool("x"); got {
		t.Error("GetBool on non-bool should return false")
	}
}

func TestOptions_GetFloat(t *testing.T) {
	o := NewOptions()
	o.Set("ratio", 3.14)

	if got := o.GetFloat("ratio"); got != 3.14 {
		t.Errorf("GetFloat = %f, want 3.14", got)
	}
}

func TestOptions_GetFloat_FromInt(t *testing.T) {
	o := NewOptions()
	o.Set("n", 10)

	if got := o.GetFloat("n"); got != 10.0 {
		t.Errorf("GetFloat from int = %f, want 10.0", got)
	}
}

func TestOptions_GetFloat_Missing_ReturnsZero(t *testing.T) {
	o := NewOptions()
	if got := o.GetFloat("missing"); got != 0 {
		t.Errorf("GetFloat(missing) = %f, want 0", got)
	}
}

func TestOptions_DefaultNotOverriddenBySet(t *testing.T) {
	o := NewOptions()
	o.SetDefault("x", "default")
	o.Set("y", "other") // different key

	if got := o.GetString("x"); got != "default" {
		t.Errorf("default should survive unrelated Set; got %q", got)
	}
}

func TestOptions_GetInt_NonIntegerValue(t *testing.T) {
	o := NewOptions()
	o.Set("name", "alice")

	if got := o.GetInt("name"); got != 0 {
		t.Errorf("GetInt on string value = %d, want 0", got)
	}
}

func TestOptions_GetInt_FromInt64(t *testing.T) {
	o := NewOptions()
	o.Set("big", int64(999))

	if got := o.GetInt("big"); got != 999 {
		t.Errorf("GetInt from int64 = %d, want 999", got)
	}
}

func TestOptions_GetInt_Missing_ReturnsZero(t *testing.T) {
	o := NewOptions()
	if got := o.GetInt("missing"); got != 0 {
		t.Errorf("GetInt(missing) = %d, want 0", got)
	}
}

func TestOptions_GetInt_BoolValue_ReturnsZero(t *testing.T) {
	o := NewOptions()
	o.Set("flag", true)

	if got := o.GetInt("flag"); got != 0 {
		t.Errorf("GetInt on bool = %d, want 0", got)
	}
}

func TestOptions_GetFloat_NonFloatValue(t *testing.T) {
	o := NewOptions()
	o.Set("name", "alice")

	if got := o.GetFloat("name"); got != 0 {
		t.Errorf("GetFloat on string value = %f, want 0", got)
	}
}

func TestOptions_GetFloat_FromFloat32(t *testing.T) {
	o := NewOptions()
	o.Set("f32", float32(2.5))

	got := o.GetFloat("f32")
	if got < 2.4 || got > 2.6 {
		t.Errorf("GetFloat from float32 = %f, want ~2.5", got)
	}
}

func TestOptions_GetFloat_FromInt64(t *testing.T) {
	o := NewOptions()
	o.Set("big", int64(42))

	if got := o.GetFloat("big"); got != 42.0 {
		t.Errorf("GetFloat from int64 = %f, want 42.0", got)
	}
}

func TestOptions_GetFloat_BoolValue_ReturnsZero(t *testing.T) {
	o := NewOptions()
	o.Set("flag", true)

	if got := o.GetFloat("flag"); got != 0 {
		t.Errorf("GetFloat on bool = %f, want 0", got)
	}
}

func TestOptions_GetBool_Missing_ReturnsFalse(t *testing.T) {
	o := NewOptions()
	if got := o.GetBool("missing"); got {
		t.Error("GetBool(missing) should return false")
	}
}

func TestOptions_GetString_Missing_ReturnsEmpty(t *testing.T) {
	o := NewOptions()
	if got := o.GetString("missing"); got != "" {
		t.Errorf("GetString(missing) = %q, want empty", got)
	}
}

func TestOptions_SetOverridesSetDefault(t *testing.T) {
	o := NewOptions()
	o.SetDefault("key", "default_val")
	o.Set("key", "explicit_val")

	if got := o.GetString("key"); got != "explicit_val" {
		t.Errorf("Set should override SetDefault; got %q", got)
	}
}

func TestOptions_GetString_FromDefault(t *testing.T) {
	o := NewOptions()
	o.SetDefault("ua", "agent/1.0")

	if got := o.GetString("ua"); got != "agent/1.0" {
		t.Errorf("GetString from default = %q, want %q", got, "agent/1.0")
	}
}

package protocol

import "testing"

func TestInputValidate(t *testing.T) {
	ok := []InputEvent{
		{T: "move", X: 0, Y: 0}, {T: "move", X: 1, Y: 1},
		{T: "down", B: 2}, {T: "wheel", D: 120}, {T: "wheel", D: -1200},
		{T: "keydown", K: 1}, {T: "keyup", K: 254},
	}
	for _, e := range ok {
		if err := e.Validate(); err != nil {
			t.Errorf("%+v: beklenmedik hata %v", e, err)
		}
	}
	bad := []InputEvent{
		{T: "move", X: 1.5}, {T: "move", Y: -0.1}, {T: "down", B: 3},
		{T: "wheel", D: 5000}, {T: "keydown", K: 0}, {T: "keydown", K: 255},
		{T: "zıpla"},
	}
	for _, e := range bad {
		if err := e.Validate(); err == nil {
			t.Errorf("%+v: hata bekleniyordu", e)
		}
	}
}

package main

import "testing"

func TestSGRFromConsoleMouse_PressRelease(t *testing.T) {
	seq, state := sgrFromConsoleMouse(0, conLeftButton, 0, 0, 0, 0)
	if string(seq) != "\x1b[<0;1;1M" {
		t.Fatalf("press = %q", seq)
	}
	if state != conLeftButton {
		t.Fatalf("state = %#x", state)
	}
	seq, state = sgrFromConsoleMouse(state, 0, 0, 0, 4, 9)
	if string(seq) != "\x1b[<0;5;10m" {
		t.Fatalf("release = %q", seq)
	}
	if state != 0 {
		t.Fatalf("state after release = %#x", state)
	}
}

func TestSGRFromConsoleMouse_RightMiddle(t *testing.T) {
	seq, _ := sgrFromConsoleMouse(0, conRightButton, 0, 0, 2, 2)
	if string(seq) != "\x1b[<2;3;3M" {
		t.Fatalf("right = %q", seq)
	}
	seq, _ = sgrFromConsoleMouse(0, conMiddleButton, 0, 0, 2, 2)
	if string(seq) != "\x1b[<1;3;3M" {
		t.Fatalf("middle = %q", seq)
	}
}

func TestSGRFromConsoleMouse_DoubleClickIsPress(t *testing.T) {
	seq, _ := sgrFromConsoleMouse(0, conLeftButton, conDoubleClick, 0, 1, 1)
	if string(seq) != "\x1b[<0;2;2M" {
		t.Fatalf("double click = %q", seq)
	}
}

func TestSGRFromConsoleMouse_Drag(t *testing.T) {
	seq, _ := sgrFromConsoleMouse(conLeftButton, conLeftButton, conMouseMoved, 0, 7, 3)
	if string(seq) != "\x1b[<32;8;4M" {
		t.Fatalf("drag = %q", seq)
	}
}

func TestSGRFromConsoleMouse_MotionWithoutButtonDropped(t *testing.T) {
	seq, _ := sgrFromConsoleMouse(0, 0, conMouseMoved, 0, 7, 3)
	if seq != nil {
		t.Fatalf("motion = %q, want nil", seq)
	}
}

func TestSGRFromConsoleMouse_Wheel(t *testing.T) {
	up := uint32(120) << 16
	seq, _ := sgrFromConsoleMouse(0, up, conMouseWheeled, 0, 0, 0)
	if string(seq) != "\x1b[<64;1;1M" {
		t.Fatalf("wheel up = %q", seq)
	}
	down := uint32(uint16(-120&0xffff)) << 16
	seq, _ = sgrFromConsoleMouse(0, down, conMouseWheeled, 0, 0, 0)
	if string(seq) != "\x1b[<65;1;1M" {
		t.Fatalf("wheel down = %q", seq)
	}
}

func TestSGRFromConsoleMouse_Modifiers(t *testing.T) {
	seq, _ := sgrFromConsoleMouse(0, conLeftButton, 0,
		conShiftPressed|conLeftCtrlPressed, 0, 0)
	if string(seq) != "\x1b[<20;1;1M" { // 0 + shift 4 + ctrl 16
		t.Fatalf("modified press = %q", seq)
	}
}

func TestSGRFromConsoleMouse_WheelKeepsHeldState(t *testing.T) {
	up := uint32(120)<<16 | conLeftButton
	_, state := sgrFromConsoleMouse(conLeftButton, up, conMouseWheeled, 0, 0, 0)
	if state != conLeftButton {
		t.Fatalf("state = %#x, want left held", state)
	}
}

package deploy

import "testing"

func TestBuildCreateStageTagName_Format(t *testing.T) {
	t.Parallel()

	got := buildCreateStageTagName("ydyl", "op", 1)
	want := "ydyl-op-create-1"
	if got != want {
		t.Fatalf("unexpected create-stage tag name: got=%s want=%s", got, want)
	}
}

func TestBuildCreateStageTagName_RelaunchOrdinalKeepsIncreasing(t *testing.T) {
	t.Parallel()

	// 首轮创建 3 台后补 2 台，补机索引应从 4 开始。
	firstRoundLast := buildCreateStageTagName("ydyl", "xjst", 3)
	relaunchFirst := buildCreateStageTagName("ydyl", "xjst", 4)
	relaunchSecond := buildCreateStageTagName("ydyl", "xjst", 5)

	if firstRoundLast != "ydyl-xjst-create-3" {
		t.Fatalf("unexpected first round last name: %s", firstRoundLast)
	}
	if relaunchFirst != "ydyl-xjst-create-4" {
		t.Fatalf("unexpected relaunch first name: %s", relaunchFirst)
	}
	if relaunchSecond != "ydyl-xjst-create-5" {
		t.Fatalf("unexpected relaunch second name: %s", relaunchSecond)
	}
}

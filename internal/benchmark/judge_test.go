package benchmark

import "testing"

func TestExtractJudgeJSONFromFencedBlock(t *testing.T) {
	input := "```json\n{\"winner\":\"a\",\"ranking\":[\"a\"],\"scores\":{\"a\":{\"correctness\":9,\"completeness\":9,\"usefulness\":9,\"style\":9}},\"summary\":\"ok\"}\n```"
	got := extractJudgeJSON(input)
	want := "{\"winner\":\"a\",\"ranking\":[\"a\"],\"scores\":{\"a\":{\"correctness\":9,\"completeness\":9,\"usefulness\":9,\"style\":9}},\"summary\":\"ok\"}"
	if got != want {
		t.Fatalf("extractJudgeJSON()=%q want %q", got, want)
	}
}

func TestExtractJudgeJSONFromWrappedText(t *testing.T) {
	input := "Here is the result:\n```json\n{\"winner\":\"a\",\"ranking\":[\"a\",\"b\"],\"scores\":{},\"summary\":\"ok\"}\n```\nThanks."
	got := extractJudgeJSON(input)
	want := "{\"winner\":\"a\",\"ranking\":[\"a\",\"b\"],\"scores\":{},\"summary\":\"ok\"}"
	if got != want {
		t.Fatalf("extractJudgeJSON()=%q want %q", got, want)
	}
}

func TestExtractJudgeJSONBalancedObjectIgnoresTrailingText(t *testing.T) {
	input := "prefix {\"winner\":\"a\",\"ranking\":[\"a\"],\"scores\":{\"a\":{\"correctness\":9,\"completeness\":8,\"usefulness\":7,\"style\":6}},\"summary\":\"ok\"} trailing"
	got := extractJudgeJSON(input)
	want := "{\"winner\":\"a\",\"ranking\":[\"a\"],\"scores\":{\"a\":{\"correctness\":9,\"completeness\":8,\"usefulness\":7,\"style\":6}},\"summary\":\"ok\"}"
	if got != want {
		t.Fatalf("extractJudgeJSON()=%q want %q", got, want)
	}
}

package provider

import "testing"

func TestEffectiveModelUsesConfiguredOrStageDefault(t *testing.T) {
	tests := []struct {
		provider   string
		configured string
		stage      string
		want       string
	}{
		{provider: "openai", configured: "custom", stage: "analysis", want: "custom"},
		{provider: "openai", stage: "transcribe", want: openaiTranscribeModel},
		{provider: "openai", stage: "analysis", want: openaiAnalysisModel},
		{provider: "groq", stage: "transcribe", want: groqTranscribeModel},
		{provider: "groq", stage: "analysis", want: groqAnalysisModel},
	}
	for _, tt := range tests {
		if got := EffectiveModel(tt.provider, tt.configured, tt.stage); got != tt.want {
			t.Errorf("EffectiveModel(%q, %q, %q)=%q, want %q", tt.provider, tt.configured, tt.stage, got, tt.want)
		}
	}
}

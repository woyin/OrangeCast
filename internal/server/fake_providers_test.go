package server

import (
	"github.com/woyin/orangecast/internal/provider"
)

// ---- Fake provider: Paraphrase ----

type fakeParaphrase struct {
	result *provider.ParaphraseResult
	err    error
}

func (f *fakeParaphrase) Paraphrase(question string, refs []provider.Segment) (*provider.ParaphraseResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}
func (f *fakeParaphrase) Name() string { return "fake-paraphrase" }

// ---- Fake provider: StudyChat ----

type fakeStudyChat struct {
	result *provider.StudyChatResult
	err    error
}

func (f *fakeStudyChat) StudyChatAnswer(question string, history []provider.StudyChatMessage, candidates []provider.Segment) (*provider.StudyChatResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}
func (f *fakeStudyChat) Name() string { return "fake-studychat" }

// ---- Fake provider: ReferenceCheck ----

type fakeRefChecker struct {
	result provider.ReferenceCheckResult
	err    error
}

func (f *fakeRefChecker) CheckReference(question, answer string, refs []provider.Segment) (provider.ReferenceCheckResult, error) {
	if f.err != nil {
		return provider.ReferenceCheckResult{}, f.err
	}
	return f.result, nil
}
func (f *fakeRefChecker) Name() string { return "fake-refcheck" }

// fakeBundleFor 构造一个可注入的 bundleFor 函数，返回带 fake providers 的 bundle。
func fakeBundleFor(paraphrase *fakeParaphrase, studyChat *fakeStudyChat, refCheck *fakeRefChecker) func(provider.TaskConfig) (*provider.ProviderBundle, error) {
	return func(tc provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			Paraphrase: paraphrase,
			StudyChat:  studyChat,
			RefChecker: refCheck,
		}, nil
	}
}

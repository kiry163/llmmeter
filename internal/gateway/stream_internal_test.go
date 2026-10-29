package gateway

import "testing"

// TestTrackerOnlyForUsageClasses pins the rule that only chat and responses
// responses are buffered for usage extraction. Buffering any other class would
// hold up to 10MiB of audio or model output per request in memory.
func TestTrackerOnlyForUsageClasses(t *testing.T) {
	for _, class := range []string{classChat, classResponses} {
		if trackerForClass(class, false) == nil {
			t.Fatalf("%s: want a tracker", class)
		}
	}
	for _, class := range []string{classModels, classAudio, classOther} {
		if trackerForClass(class, false) != nil {
			t.Fatalf("%s: want no tracker", class)
		}
	}
}

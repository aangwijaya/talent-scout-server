# Fluency signals are advisory, never scored

In a voice interview the server measures how the applicant speaks (pace, pauses, fillers, transcription confidence, detected language, plus a short model note on grammar and vocabulary). We show these to the recruiter as a "Spoken English" card and keep them out of the rubric scores and `overall_score`. Speech-to-text is least accurate on non-native accents, so an automatic fluency score would penalise the very applicants the feature is meant to assess fairly; the recruiter listens to the recordings and decides instead.

## Considered options

- **A `spoken_english` rubric criterion** scored by the model: rejected, because transcription errors on accents would turn into score bias with no human in between.
- **A pronunciation assessment service** (about $0.43 per interview): rejected for cost, roughly ten times the per-interview budget, and it would still be an automatic judgement.

## Consequences

- Making fluency a scored criterion later is a new decision, taken only once real interviews show the signals are fair across accents.
- The authenticity warning stays separate: fluency describes how someone speaks, authenticity whether the answers are their own.

package graph

// FakeReplies are the deterministic LLM replies used when LLM_MODE=fake. Keys
// are graph node ids because several nodes share one model. The planner reply
// is a valid plan for the seed job (config/seed-job.yaml): 3 topics covering all
// four rubric criteria. The scorer reply quotes the deterministic fake
// interview transcript (the two applicant answers: "I moved the state into a
// separate store so each page stayed small and easy to test." and "Props were
// crossing four layers, and I explained the tradeoff to the team before we
// changed it.") by alias, so a fake-mode run ends in review with valid scores.
var FakeReplies = map[string]string{
	"planner": `{"topics":[
		{"title":"Frontend depth","goal":"Assess Svelte/React, state management, rendering and performance experience.","rubric_keys":["frontend_depth"],"seed_question":"Walk me through a frontend performance problem you solved and how you measured it."},
		{"title":"System design","goal":"Assess component boundaries, data flow, API contracts and trade-offs.","rubric_keys":["system_design"],"seed_question":"How would you split a complex page into components and decide what state belongs where?"},
		{"title":"Testing, quality and communication","goal":"Assess testing, accessibility, attention to detail and clarity of explanation.","rubric_keys":["testing_quality","communication"],"seed_question":"Tell me how you test an interactive UI, including accessibility, and how you explain trade-offs to teammates."}
	]}`,
	"interviewer": "Tell me about a frontend performance problem you solved. What did you measure before and after, and what trade-offs did you accept?",
	"judge":       `{"verdict":"follow_up","reason":"The answer needs a concrete example with measured outcomes.","missing":["specific project","before/after metrics"]}`,
	"scorer": `{"scores":[
		{"rubric_key":"frontend_depth","score":4,"rationale":"Moved shared state into a store with a concrete reason.","evidence":[{"message_id":"m1","quote":"I moved the state into a separate store"}]},
		{"rubric_key":"system_design","score":3,"rationale":"Reduced prop drilling after naming the layers.","evidence":[{"message_id":"m2","quote":"Props were crossing four layers"}]},
		{"rubric_key":"testing_quality","score":4,"rationale":"Kept pages small and easy to test.","evidence":[{"message_id":"m1","quote":"each page stayed small and easy to test"}]},
		{"rubric_key":"communication","score":4,"rationale":"Explained the tradeoff before changing the design.","evidence":[{"message_id":"m2","quote":"I explained the tradeoff to the team before we changed it"}]}
	]}`,
}

package graph

// FakeReplies are the deterministic LLM replies used when LLM_MODE=fake. Keys
// are graph node ids because several nodes share one model. The planner reply
// is a valid plan for the seed job (config/seed-job.yaml): 3 topics covering all
// four rubric criteria. server#10/server#11 add judge and scorer entries.
var FakeReplies = map[string]string{
	"planner": `{"topics":[
		{"title":"Frontend depth","goal":"Assess Svelte/React, state management, rendering and performance experience.","rubric_keys":["frontend_depth"],"seed_question":"Walk me through a frontend performance problem you solved and how you measured it."},
		{"title":"System design","goal":"Assess component boundaries, data flow, API contracts and trade-offs.","rubric_keys":["system_design"],"seed_question":"How would you split a complex page into components and decide what state belongs where?"},
		{"title":"Testing, quality and communication","goal":"Assess testing, accessibility, attention to detail and clarity of explanation.","rubric_keys":["testing_quality","communication"],"seed_question":"Tell me how you test an interactive UI, including accessibility, and how you explain trade-offs to teammates."}
	]}`,
	"interviewer": "Tell me about a frontend performance problem you solved. What did you measure before and after, and what trade-offs did you accept?",
}

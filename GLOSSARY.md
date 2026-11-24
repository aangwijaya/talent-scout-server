# Talent Scout

The language of the product: how a recruiter screens applicants with an AI interview. Terms about how agents build this repo live in the control plane's glossary, not here.

## People

**Recruiter**:
The one person who creates jobs, reads results and decides on applicants.
_Avoid_: admin, user, HR

**Applicant**:
A person who applied to a job and is interviewed without an account.
_Avoid_: candidate, user

## Interview

**Job**:
An opening with a description and a rubric that applicants apply to.
_Avoid_: vacancy, posting, loker

**Session**:
One applicant's interview for one application, opened by a one-time link and limited in time.
_Avoid_: interview (for the stored record), chat

**Answer**:
One applicant turn in a session, typed or spoken; its text is what the scorer quotes.
_Avoid_: reply, response, message (when only the applicant's turn is meant)

**Authenticity warning**:
An advisory note to the recruiter on whether answers may not be the applicant's own work; it never changes a score or rejects anyone.
_Avoid_: cheating flag, AI detection, fraud score

## Voice

**Spoken English requirement**:
The recruiter's setting on a job saying fluent spoken English is required; it is what turns on a voice interview.
_Avoid_: voice job, English test

**Voice interview**:
A session in which the interviewer's questions are read aloud and the applicant answers by speaking.
_Avoid_: call, phone screen, audio mode

**Spoken answer**:
An answer the applicant recorded; its text is the transcript the system made, which the applicant cannot edit.
_Avoid_: voice message, audio answer

**Recording**:
The audio of a spoken answer, kept for a limited time with the applicant's consent so the recruiter can listen.
_Avoid_: audio file, clip

**Text fallback**:
A typed answer inside a voice interview, always allowed and marked with its reason for the recruiter; it never rejects anyone.
_Avoid_: text mode, opt-out

**Fluency signals**:
Advisory measures of how the applicant speaks English, shown to the recruiter as "Spoken English"; they never change a score.
_Avoid_: fluency score, English score, pronunciation grade

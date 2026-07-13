# Pintia source-shape fixture provenance and sanitization

Every committed fixture contains synthetic values. No logged-in response body,
person name, student number, user/group/public identifier, problem text, or
program is committed.

## Fresh response-shape evidence

`sanitized-get-common-rankings-http-response.json` preserves the complete key
set, nesting, JSON types, and cross-object relationships observed in the
authenticated `GetCommonRankings` HTTP response captured on 2026-07-11. The
uncommitted source response had SHA-256
`39bd87591b5795e356e1a245fc73129e3d584cec0504476b15a51bb1cb69da13`.
The committed fixture reduces the collection to one entry and replaces every
value. The source response proved these material facts:

- the root is `{ commonRankings, total }`;
- `commonRankings` contains `labels`, `commonRankings`, `selfRanking`, and
  `labelByProblemSetProblemId`;
- each ranking user contains nested `studentUser`, nested `user`, `examId`, and
  `userGroupId`;
- all 45 observed ranking entries had a non-null `userGroupId`;
- ranking problem results contain `acceptTime`, `score`,
  `submitCountSnapshot`, and `validSubmitCount` as numbers.

`sanitized-get-problem-set-response.json` retains its separately documented
real response-shape provenance. Its wrapper, complete top-level key set,
complete `problemSet`, `problemSet.permission`, `problemSet.problemSetConfig`,
`exam`, and `exam.examConfig` key sets, JSON types, and array element types come
from the public Pintia response fixture
[`problem-set-exams.json`](https://github.com/jinzcdev/vscode-pintia/blob/bcac68bd7fb184e5a66ef7b8b27afd6647f0eb4d/resources/pta_template/problem-set-exams.json).
All values in the committed copy are replacements.

Pintia's current authenticated problem-set, ranking, and submission routes were
inspected on 2026-07-13. The observed production chunks and their complete
downloaded-byte SHA-256 values were:

- `44531.6abdd6502d21fffe8937.chunk.js`:
  `b870d9d9c98724e3944922aa62d72cd506d8d61a5064e20b6ebcf1452a00cd42`;
- `12969.c1107950827e4c95c7a0.chunk.js`:
  `73892d671277723922785272fd801ba71d62b3a334d44c2ada90eba698a94ee1`;
- `68813.308c42038f53672bfdf3.chunk.js`:
  `1ae14eacabc1251440f961f928e5077fd4a9e4b919517d939cae66b86cd248b0`.

Those chunks define the six read operations used by the exporter:

- `GetProblemSet`: `GET /api/problem-sets/{problem_set_id}`;
- `ListProblemSetProblems`:
  `GET /api/problem-sets/{problem_set_id}/problems`;
- `ListUserGroupsForProblemSet`:
  `GET /api/problem-sets/{problem_set_id}/user-groups`;
- `GetCommonRankings`:
  `GET /api/problem-sets/{problem_set_id}/common-rankings`;
- `ListSubmissions`: `GET /api/problem-sets/{problem_set_id}/submissions`;
- `GetSubmission`: `GET /api/submissions/{submission_id}`.

The ranking-page consumer still reads normalized `commonRankings`, `userById`,
and `studentUserById` fields, while user-group consumers map
`userGroupById[id].name`. The exporter resolves the named Webpack exports and
does not embed observed module ids or minified export names.

## Synthetic operation fixtures

Fresh successful response bodies were unavailable for the following named
operations. Their committed fixtures are deliberately prefixed `synthetic-`
and contain only the fields consumed by the existing adapter contract:

- `synthetic-list-problem-set-problems-response.json`
- `synthetic-list-submissions-response.json`
- `synthetic-get-submission-response.json`
- `synthetic-list-user-groups-for-problem-set-response.json`

Their operation names, method/path contracts, and consumer field accesses are
supported by the captured current Webpack bundle and the previous TypeScript
collector implementation. They make no fresh-response completeness claim.
The MAIN-world collector tests execute these fixtures through the same named
function resolver and then build a strict snapshot. A future developer-only
sanitizer may replace them after a successful live capture; production builds
contain no raw-data or diagnostic export path.

`sanitized-source-shape.json` is a fully synthetic normalized aggregate used by
domain invariant tests. It is assembled from the operation fixtures and is not
described as a raw Pintia response.

## Replacement rules

- Replace every problem-set, problem, submission, user, student, exam, and
  group identifier with a synthetic token.
- Replace titles, names, nicknames, student numbers, organizations, HTML,
  timestamps, scores, limits, ranks, and metrics with synthetic values.
- Replace every program, compiler log, judge error, and checker output with an
  explicit `SANITIZED_*` placeholder.
- Reduce collections only after recording the complete key/type union.
- Re-key every index after replacing its values.
- Omit source filenames, filesystem paths, problem-set identity, title,
  collection sizes, and all free-form source content.

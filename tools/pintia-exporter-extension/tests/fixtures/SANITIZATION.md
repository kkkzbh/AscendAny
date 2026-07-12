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

Pintia's current primary bundle was captured on 2026-07-11. Chunk
`855.8ebba330ac59c40f8ac4.chunk.js` (SHA-256
`91d61034e2457f8b4da8c3ee55610cd0fd01399807f3336d8bc66211fa1e8068`)
defines `GetProblemSet` as `GET /api/problem-sets/{problem_set_id}`. Chunk
`12969` defines `GetCommonRankings` as
`GET /api/problem-sets/{problem_set_id}/common-rankings`, and its ranking-page
consumer reads the normalized `commonRankings`, `userById`, and
`studentUserById` fields. The same captured bundle defines
`ListUserGroupsForProblemSet` as
`GET /api/problem-sets/{problem_set_id}/user-groups`; consumers map
`userGroupById[id].name`. The exporter resolves these named functions rather
than embedding observed module ids.

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

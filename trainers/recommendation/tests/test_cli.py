from __future__ import annotations

from decimal import Decimal
import ast
import copy
import hashlib
import io
import json
import math
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import tomllib
import unittest
from unittest import mock


TRAINER_ROOT = Path(__file__).resolve().parents[1]
SOURCE_ROOT = TRAINER_ROOT / "src"
PACKAGE_ROOT = SOURCE_ROOT / "ascendany_recommendation_trainer"
sys.path.insert(0, str(SOURCE_ROOT))

from ascendany_recommendation_trainer import attestation, cli, contract
from ascendany_recommendation_trainer import model as model_module
from ascendany_recommendation_trainer.train import train


def number(value: float) -> Decimal:
    return Decimal(repr(float(value)))


def digest(value: object) -> str:
    return hashlib.sha256(contract.canonical_json(value)).hexdigest()


def runtime_attestation() -> dict[str, str]:
    return {
        "hostCapabilitySha256": "4" * 64,
        "runtimeAttestationSha256": "5" * 64,
        "runtimeConstructionSha256": "1" * 64,
        "runtimeProvenanceSha256": "2" * 64,
        "runtimeTreeSha256": "3" * 64,
    }


def run_training(value: dict[str, object], manifest_sha256: str) -> dict[str, object]:
    return train(value, manifest_sha256, runtime_attestation())


def interaction(
    *,
    actor_id: str,
    problem_key: str,
    snapshot_id: str,
    target: float,
    submissions: int,
    timestamp: str,
    split: str,
) -> dict[str, object]:
    interaction_id = digest(
        {"actorId": actor_id, "problemKey": problem_key, "snapshotId": snapshot_id}
    )
    return {
        "interactionId": interaction_id,
        "snapshotId": snapshot_id,
        "actorId": actor_id,
        "problemKey": problem_key,
        "firstSubmittedAt": timestamp,
        "lastSubmittedAt": timestamp,
        "submissionCount": Decimal(submissions),
        "validSubmissionCount": Decimal(submissions),
        "targetScoreRate": number(target),
        "passed": target == 1.0,
        "split": split,
    }


def actor_features(items: list[dict[str, object]]) -> list[Decimal]:
    training = [item for item in items if item["split"] == "train"]
    return [
        number(math.log1p(len(training))),
        number((sum(bool(item["passed"]) for item in training) + 1) / (len(training) + 2)),
        number(math.fsum(float(item["targetScoreRate"]) for item in training) / len(training)),
        number(
            math.fsum(math.log1p(int(item["submissionCount"])) for item in training)
            / len(training)
        ),
    ]


def problem_features(items: list[dict[str, object]]) -> tuple[list[Decimal], int, int]:
    training = [item for item in items if item["split"] == "train"]
    acceptance = (sum(bool(item["passed"]) for item in training) + 1) / (len(training) + 2)
    actor_count = len({str(item["actorId"]) for item in training})
    submission_count = sum(int(item["submissionCount"]) for item in training)
    return (
        [
            number(math.log(acceptance / (1 - acceptance))),
            number(math.log1p(actor_count)),
            number(math.log1p(submission_count)),
        ],
        actor_count,
        submission_count,
    )


def training_bundle(accelerator: str = "cpu") -> tuple[dict[str, object], bytes, str]:
    first_fact = "a" * 64
    second_fact = "b" * 64
    first_key = f"pintia:1001:{first_fact}"
    second_key = f"pintia:1002:{second_fact}"
    interactions = [
        interaction(
            actor_id="11",
            problem_key=first_key,
            snapshot_id="101",
            target=1.0,
            submissions=1,
            timestamp="2026-01-01T00:00:00Z",
            split="train",
        ),
        interaction(
            actor_id="11",
            problem_key=second_key,
            snapshot_id="102",
            target=1.0,
            submissions=2,
            timestamp="2026-01-02T00:00:00Z",
            split="train",
        ),
        interaction(
            actor_id="11",
            problem_key=first_key,
            snapshot_id="103",
            target=1.0,
            submissions=1,
            timestamp="2026-01-09T00:00:00Z",
            split="validation",
        ),
        interaction(
            actor_id="29",
            problem_key=first_key,
            snapshot_id="201",
            target=0.0,
            submissions=3,
            timestamp="2026-01-03T00:00:00Z",
            split="train",
        ),
        interaction(
            actor_id="29",
            problem_key=second_key,
            snapshot_id="202",
            target=0.0,
            submissions=1,
            timestamp="2026-01-04T00:00:00Z",
            split="train",
        ),
        interaction(
            actor_id="29",
            problem_key=second_key,
            snapshot_id="203",
            target=0.0,
            submissions=2,
            timestamp="2026-01-10T00:00:00Z",
            split="validation",
        ),
        interaction(
            actor_id="47",
            problem_key=first_key,
            snapshot_id="301",
            target=1.0,
            submissions=2,
            timestamp="2026-01-05T00:00:00Z",
            split="train",
        ),
        interaction(
            actor_id="47",
            problem_key=second_key,
            snapshot_id="302",
            target=0.0,
            submissions=4,
            timestamp="2026-01-06T00:00:00Z",
            split="train",
        ),
        interaction(
            actor_id="47",
            problem_key=first_key,
            snapshot_id="303",
            target=1.0,
            submissions=1,
            timestamp="2026-01-11T00:00:00Z",
            split="validation",
        ),
    ]
    interactions.sort(key=lambda item: str(item["interactionId"]))

    configuration_document = {
        "algorithm": contract.ALGORITHM,
        "knowledgeCatalogVersionId": "51",
        "accelerator": accelerator,
        "seed": Decimal(17),
        "epochs": Decimal(40),
        "patience": Decimal(10),
        "batchSize": Decimal(3),
        "learningRate": Decimal("0.05"),
        "weightDecay": Decimal("0.001"),
        "minTrainInteractions": Decimal(6),
        "minActorInteractions": Decimal(3),
        "minProblemInteractions": Decimal(2),
        "validation": {
            "minActors": Decimal(3),
            "minInteractions": Decimal(3),
            "minRelativeLogLossImprovement": Decimal(0),
        },
        "pathPolicy": {
            "targetMastery": Decimal("0.8"),
            "maxKnowledgeTargets": Decimal(2),
            "minSteps": Decimal(2),
            "maxSteps": Decimal(4),
            "problemsPerStep": Decimal(2),
            "targetSuccessProbability": Decimal("0.7"),
        },
        "rankingWeights": {"knowledgeGap": Decimal(1), "successDistance": Decimal(1)},
    }
    knowledge_points = [
        {
            "id": "arrays",
            "label": "Arrays",
            "description": "Indexed contiguous data.",
            "prerequisiteIds": [],
        },
        {
            "id": "dynamic-programming",
            "label": "Dynamic programming",
            "description": "State transitions over subproblems.",
            "prerequisiteIds": ["arrays"],
        },
    ]
    first_weights = [{"knowledgePointId": "arrays", "weight": Decimal(1)}]
    second_weights = [{"knowledgePointId": "dynamic-programming", "weight": Decimal(1)}]
    catalog_document = {
        "taxonomyId": "ascendany-curriculum-2026",
        "knowledgePoints": copy.deepcopy(knowledge_points),
        "problemAssignments": [
            {
                "platform": "pintia",
                "problemId": "1001",
                "problemFactSha256": first_fact,
                "knowledge": copy.deepcopy(first_weights),
            },
            {
                "platform": "pintia",
                "problemId": "1002",
                "problemFactSha256": second_fact,
                "knowledge": copy.deepcopy(second_weights),
            },
        ],
    }
    actors = []
    for actor_id, rating in (("11", 1200.0), ("29", 800.0), ("47", 1500.0)):
        items = [item for item in interactions if item["actorId"] == actor_id]
        actors.append(
            {"actorId": actor_id, "currentRating": number(rating), "features": actor_features(items)}
        )
    problems = []
    for problem_id, fact, problem_key, title, weights in (
        ("1001", first_fact, first_key, "Array sum", first_weights),
        ("1002", second_fact, second_key, "Knapsack", second_weights),
    ):
        items = [item for item in interactions if item["problemKey"] == problem_key]
        features, train_actor_count, train_submission_count = problem_features(items)
        problems.append(
            {
                "problemKey": problem_key,
                "sourceProblemKey": f"pintia:{problem_id}",
                "problemFactSha256": fact,
                "platform": "pintia",
                "problemId": problem_id,
                "title": title,
                "statementText": f"Solve {title}.",
                "sourceProblemSets": [
                    {
                        "problemSetId": "9001",
                        "sourceUrl": f"https://pintia.cn/problem-sets/9001/problems/{problem_id}",
                    }
                ],
                "maxScore": Decimal(100),
                "timeLimitMs": Decimal(1000),
                "memoryLimitBytes": Decimal(268435456),
                "knowledgeWeights": copy.deepcopy(weights),
                "features": features,
                "trainActorCount": Decimal(train_actor_count),
                "trainSubmissionCount": Decimal(train_submission_count),
            }
        )
    feature_schema = {
        "actorFeatureIds": list(contract.ACTOR_FEATURE_IDS),
        "problemFeatureIds": list(contract.PROBLEM_FEATURE_IDS),
    }
    analytics_manifest = {"generation": "fixture", "snapshots": ["101", "201", "301"]}
    training_provenance = {
        "versionId": "41",
        "key": "recommendation.training.default",
        "versionNumber": Decimal(3),
        "schemaId": contract.CONFIGURATION_SCHEMA,
        "documentSha256": digest(configuration_document),
    }
    catalog_provenance = {
        "versionId": "51",
        "key": "recommendation.knowledge.default",
        "versionNumber": Decimal(2),
        "schemaId": contract.CATALOG_SCHEMA,
        "documentSha256": digest(catalog_document),
    }
    manifest = {
        "protocol": contract.INPUT_PROTOCOL,
        "source": {
            "analyticsGenerationId": "73",
            "analyticsHeadRevision": Decimal(9),
            "analyticsInputManifestSha256": digest(analytics_manifest),
            "algorithmVersion": "analytics_v2",
            "analyticsConfigurationSha256": "c" * 64,
        },
        "trainingConfiguration": training_provenance,
        "knowledgeCatalog": catalog_provenance,
        "featureSchemaSha256": digest(feature_schema),
        "knowledgePointCount": Decimal(len(knowledge_points)),
        "knowledgePointSetSha256": digest(
            {"knowledgePointIds": [item["id"] for item in knowledge_points]}
        ),
        "actorCount": Decimal(len(actors)),
        "actorSetSha256": digest({"actorIds": [item["actorId"] for item in actors]}),
        "problemCount": Decimal(len(problems)),
        "problemSetSha256": digest({"problemKeys": [item["problemKey"] for item in problems]}),
        "interactionCount": Decimal(len(interactions)),
        "interactionSetSha256": digest(
            {"interactionIds": [item["interactionId"] for item in interactions]}
        ),
        "trainInteractionCount": Decimal(
            sum(item["split"] == "train" for item in interactions)
        ),
        "validationInteractionCount": Decimal(
            sum(item["split"] == "validation" for item in interactions)
        ),
        "splitSha256": digest(
            {
                "interactions": [
                    {"interactionId": item["interactionId"], "split": item["split"]}
                    for item in interactions
                ]
            }
        ),
    }
    value = {
        "protocol": contract.INPUT_PROTOCOL,
        "manifest": manifest,
        "analyticsInputManifest": analytics_manifest,
        "featureSchema": feature_schema,
        "trainingConfiguration": {
            "schemaId": contract.CONFIGURATION_SCHEMA,
            "document": configuration_document,
        },
        "knowledgeCatalog": {"schemaId": contract.CATALOG_SCHEMA, "document": catalog_document},
        "actors": actors,
        "knowledgePoints": knowledge_points,
        "problems": problems,
        "interactions": interactions,
    }
    manifest_sha256 = digest(manifest)
    return value, contract.canonical_json(value), manifest_sha256


def trainer_environment(output_directory: str, manifest_sha256: str) -> dict[str, str]:
    return {
        "ASCENDANY_TRAINER_INPUT_MANIFEST_SHA256": manifest_sha256,
        "ASCENDANY_TRAINER_EXPECTED_HOST_CAPABILITY_SHA256": "4" * 64,
        "ASCENDANY_TRAINER_EXPECTED_RUNTIME_CONSTRUCTION_SHA256": "1" * 64,
        "ASCENDANY_TRAINER_EXPECTED_RUNTIME_PROVENANCE_SHA256": "2" * 64,
        "ASCENDANY_TRAINER_EXPECTED_RUNTIME_TREE_SHA256": "3" * 64,
        "ASCENDANY_TRAINER_RUNTIME_ROOT": "/opt/ascendany-trainer-runtime/current",
        "ASCENDANY_TRAINER_MAX_INPUT_BYTES": str(4 << 20),
        "ASCENDANY_TRAINER_MAX_OUTPUT_BYTES": str(4 << 20),
        "ASCENDANY_TRAINER_OUTPUT_DIR": output_directory,
        "HOME": "/nonexistent",
        "LANG": "C.UTF-8",
        "LC_ALL": "C.UTF-8",
        "PWD": output_directory,
        "PYTHONHASHSEED": "0",
        "TZ": "UTC",
        "OMP_NUM_THREADS": "1",
    }


def production_style_python_command(program: str) -> list[str]:
    return [sys.executable, "-B", "-s", "-P", "-c", program]


def production_style_trainer_command(package_root: Path) -> list[str]:
    bootstrap = (
        "import runpy,sys;sys.path.insert(0,"
        + json.dumps(str(package_root))
        + ");runpy.run_module(\"ascendany_recommendation_trainer\","
        + "run_name=\"__main__\",alter_sys=True)"
    )
    return production_style_python_command(bootstrap)


class CanonicalJSONTests(unittest.TestCase):
    def test_canonical_encoding_matches_go_contract(self) -> None:
        first = contract.decode_canonical_object(
            b'{"a":1,"escaped":"\\u003c\\u0026\\u003e","nested":{"zero":0},"z":2}'
        )
        self.assertEqual(
            contract.canonical_json(first),
            b'{"a":1,"escaped":"\\u003c\\u0026\\u003e","nested":{"zero":0},"z":2}',
        )
        self.assertEqual(contract.canonical_json({"value": Decimal("-0.000")}), b'{"value":0}')
        self.assertEqual(contract.canonical_json({"value": Decimal("1e-4")}), b'{"value":0.0001}')

    def test_rejects_duplicate_noncanonical_and_unbounded_numbers(self) -> None:
        for value in (
            b'{"a":1,"a":2}',
            b'{"z":2, "a":1}',
            b'{"value":1e8193}',
            b'{"value":1e-4097}',
            b'{"value":"\\ud800"}',
            b'{"value":NaN}',
        ):
            with self.subTest(value=value):
                with self.assertRaises(contract.TrainerInputError):
                    contract.decode_canonical_object(value)


class TrainingContractTests(unittest.TestCase):
    def test_environment_rejects_noncanonical_compute_values(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            os.chmod(directory, 0o700)
            base = trainer_environment(directory, "a" * 64)
            for key, value in (
                ("CUDA_VISIBLE_DEVICES", "1"),
                ("MKL_NUM_THREADS", "0"),
                ("OMP_NUM_THREADS", "08"),
                ("OPENBLAS_NUM_THREADS", "257"),
            ):
                environment = dict(base)
                environment[key] = value
                with self.subTest(key=key, value=value):
                    with self.assertRaises(contract.TrainerInputError):
                        cli.validate_environment(environment)

    def test_accepts_catalog_superset_zero_limits_and_canonical_source_order(self) -> None:
        value, _, _ = training_bundle()
        catalog_document = value["knowledgeCatalog"]["document"]
        catalog_document["problemAssignments"].append(
            {
                "platform": "pintia",
                "problemId": "1003",
                "problemFactSha256": "c" * 64,
                "knowledge": [{"knowledgePointId": "arrays", "weight": Decimal(1)}],
            }
        )
        value["manifest"]["knowledgeCatalog"]["documentSha256"] = digest(catalog_document)
        value["problems"][0]["timeLimitMs"] = Decimal(0)
        value["problems"][0]["memoryLimitBytes"] = Decimal(0)
        source_url = value["problems"][0]["sourceProblemSets"][0]["sourceUrl"]
        value["problems"][0]["sourceProblemSets"] = [
            {"problemSetId": "9001", "sourceUrl": source_url},
            {"problemSetId": "9001", "sourceUrl": source_url + "?view=full"},
            {
                "problemSetId": "10000000000000000000",
                "sourceUrl": "https://pintia.cn/problem-sets/10000000000000000000/problems/1001",
            },
        ]

        validated = contract.validate_input_bundle(value, digest(value["manifest"]))
        self.assertEqual(len(validated.problems), 2)

    def test_rejects_nonzero_number_that_underflows_float64(self) -> None:
        value, _, _ = training_bundle()
        configuration = value["trainingConfiguration"]["document"]
        configuration["learningRate"] = Decimal("1e-400")
        value["manifest"]["trainingConfiguration"]["documentSha256"] = digest(configuration)

        with self.assertRaisesRegex(contract.TrainerInputError, "representable"):
            contract.validate_input_bundle(value, digest(value["manifest"]))

    def test_nanosecond_validation_order_is_preserved(self) -> None:
        value, _, manifest_sha256 = training_bundle()
        actor_items = [item for item in value["interactions"] if item["actorId"] == "11"]
        later_train = max(
            (item for item in actor_items if item["split"] == "train"),
            key=lambda item: item["interactionId"],
        )
        validation = next(item for item in actor_items if item["split"] == "validation")
        for field in ("firstSubmittedAt", "lastSubmittedAt"):
            later_train[field] = "2026-01-09T00:00:00.000000001Z"
            validation[field] = "2026-01-09T00:00:00.000000002Z"

        validated = contract.validate_input_bundle(value, manifest_sha256)
        self.assertEqual(len(validated.validation_interactions), 3)

    def test_tiny_separable_cpu_corpus_updates_parameters_and_diagnostics(self) -> None:
        value, _, manifest_sha256 = training_bundle()
        output = run_training(value, manifest_sha256)
        self.assertEqual(output["protocol"], contract.OUTPUT_PROTOCOL)
        self.assertEqual(output["model"]["schema"], contract.MODEL_SCHEMA)
        parameters = output["model"]["parameters"]
        self.assertEqual(
            set(parameters),
            {
                "normalization",
                "studentFeatureWeights",
                "actorResiduals",
                "problemFeatureWeights",
                "problems",
            },
        )
        normalization = parameters["normalization"]
        self.assertEqual(
            set(normalization), {"actorMeans", "actorScales", "problemMeans", "problemScales"}
        )
        self.assertEqual(float(normalization["actorScales"][0]), 1.0)
        self.assertEqual(float(normalization["problemScales"][1]), 1.0)
        learned = [
            abs(float(item))
            for row in parameters["studentFeatureWeights"]
            for item in row
        ] + [
            abs(float(item))
            for actor in parameters["actorResiduals"]
            for item in actor["values"]
        ]
        self.assertTrue(any(item > 1e-10 for item in learned))
        self.assertEqual(
            output["model"]["manifest"]["parameterSha256"], digest(parameters)
        )
        diagnostics = output["model"]["diagnostics"]
        self.assertEqual(
            set(diagnostics),
            {
                "epochsCompleted",
                "bestEpoch",
                "initialTrainLogLoss",
                "finalTrainLogLoss",
                "reportedBaselineValidationLogLoss",
                "reportedValidationLogLoss",
                "reportedValidationBrier",
            },
        )
        self.assertGreaterEqual(int(diagnostics["epochsCompleted"]), int(diagnostics["bestEpoch"]))
        self.assertGreater(float(diagnostics["initialTrainLogLoss"]), 0.0)
        self.assertGreater(float(diagnostics["reportedBaselineValidationLogLoss"]), 0.0)

    def test_seed_is_byte_deterministic(self) -> None:
        value, _, manifest_sha256 = training_bundle()
        first = contract.canonical_json(run_training(copy.deepcopy(value), manifest_sha256))
        second = contract.canonical_json(run_training(copy.deepcopy(value), manifest_sha256))
        self.assertEqual(first, second)

    def test_production_python_flags_honor_hash_seed_across_processes(self) -> None:
        probe = (
            "import json,os,sys;"
            "print(json.dumps({"
            "'hash':str(hash('ascendany')),"
            "'hashRandomization':bool(sys.flags.hash_randomization),"
            "'ignoreEnvironment':bool(sys.flags.ignore_environment),"
            "'noUserSite':bool(sys.flags.no_user_site),"
            "'pythonHashSeed':os.environ.get('PYTHONHASHSEED'),"
            "'safePath':bool(sys.flags.safe_path),"
            "'sysPath':sys.path"
            "},sort_keys=True,separators=(',',':')))"
        )
        observed_hashes: set[str] = set()
        with tempfile.TemporaryDirectory() as directory:
            for _ in range(3):
                process = subprocess.run(
                    production_style_python_command(probe),
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    cwd=directory,
                    env={"PYTHONHASHSEED": "0"},
                    check=False,
                )
                self.assertEqual(process.returncode, 0, process.stderr.decode())
                self.assertEqual(process.stderr, b"")
                observation = json.loads(process.stdout)
                self.assertEqual(observation["pythonHashSeed"], "0")
                self.assertFalse(observation["hashRandomization"])
                self.assertFalse(observation["ignoreEnvironment"])
                self.assertTrue(observation["noUserSite"])
                self.assertTrue(observation["safePath"])
                self.assertNotIn("", observation["sysPath"])
                self.assertNotIn(directory, observation["sysPath"])
                observed_hashes.add(observation["hash"])
        self.assertEqual(len(observed_hashes), 1)

    def test_rejects_unknown_shape_hash_reference_and_order_drift(self) -> None:
        value, _, manifest_sha256 = training_bundle()
        invalid_cases: list[tuple[str, dict[str, object], str]] = []

        unknown = copy.deepcopy(value)
        unknown["results"] = []
        invalid_cases.append(("unknown", unknown, "invalid fields"))

        shape = copy.deepcopy(value)
        shape["actors"][0]["features"].pop()
        invalid_cases.append(("shape", shape, "invalid item count"))

        negative_rating = copy.deepcopy(value)
        negative_rating["actors"][0]["currentRating"] = Decimal(-1)
        invalid_cases.append(("negative rating", negative_rating, "below its supported range"))

        excessive_rating = copy.deepcopy(value)
        excessive_rating["actors"][0]["currentRating"] = Decimal("1000000.0000001")
        invalid_cases.append(("excessive rating", excessive_rating, "exceeds its supported range"))

        nested_unknown = copy.deepcopy(value)
        nested_unknown["trainingConfiguration"]["document"]["fallback"] = True
        invalid_cases.append(("nested unknown", nested_unknown, "invalid fields"))

        dangling = copy.deepcopy(value)
        dangling["interactions"][0]["actorId"] = "999"
        invalid_cases.append(("dangling", dangling, "dangling"))

        unordered = copy.deepcopy(value)
        unordered["problems"].reverse()
        invalid_cases.append(("unordered", unordered, "ascending"))

        for label, invalid, message in invalid_cases:
            with self.subTest(label=label):
                with self.assertRaisesRegex(contract.TrainerInputError, message):
                    run_training(invalid, manifest_sha256)

        with self.assertRaisesRegex(contract.TrainerInputError, "process contract"):
            run_training(copy.deepcopy(value), "d" * 64)

    def test_configured_cuda_unavailable_fails_directly(self) -> None:
        value, _, manifest_sha256 = training_bundle(accelerator="cuda")
        with mock.patch.object(model_module.torch.cuda, "is_available", return_value=False):
            with self.assertRaisesRegex(contract.TrainerRuntimeError, "unavailable"):
                run_training(value, manifest_sha256)

    def test_output_contains_no_business_recommendation_fields(self) -> None:
        value, _, manifest_sha256 = training_bundle()
        output = run_training(value, manifest_sha256)
        serialized = contract.canonical_json(output).decode("utf-8")
        for forbidden in (
            '"results"',
            '"recommendations"',
            '"learningPath"',
            '"knowledgeDetails"',
            '"sourceRating"',
        ):
            with self.subTest(forbidden=forbidden):
                self.assertNotIn(forbidden, serialized)

    def test_cli_writes_only_matching_private_canonical_output(self) -> None:
        _, raw, manifest_sha256 = training_bundle()
        with tempfile.TemporaryDirectory() as directory:
            os.chmod(directory, 0o700)
            environment = trainer_environment(directory, manifest_sha256)
            stdin = mock.Mock(buffer=io.BytesIO(raw))
            with mock.patch.object(sys, "stdin", stdin), mock.patch.object(
                sys, "argv", ["ascendany-recommendation-trainer"]
            ):
                output = cli.run(environment, runtime_attestation())
            output_path = Path(directory) / cli.OUTPUT_FILENAME
            self.assertEqual(output, output_path.read_bytes())
            self.assertEqual(stat.S_IMODE(output_path.stat().st_mode), 0o600)
            self.assertEqual(
                contract.canonical_json(contract.decode_canonical_object(output)),
                output,
            )
            self.assertEqual([item.name for item in Path(directory).iterdir()], [cli.OUTPUT_FILENAME])

    def test_cli_fails_closed_outside_attested_runtime_without_publication(self) -> None:
        _, raw, manifest_sha256 = training_bundle()
        with tempfile.TemporaryDirectory() as directory:
            os.chmod(directory, 0o700)
            environment = trainer_environment(directory, manifest_sha256)
            process = subprocess.run(
                production_style_trainer_command(SOURCE_ROOT),
                input=raw,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                cwd=directory,
                env=environment,
                check=False,
            )
            self.assertEqual(process.returncode, 70)
            self.assertEqual(process.stdout, b"")
            self.assertIn(b"trainer runtime capability failure", process.stderr)
            self.assertEqual(list(Path(directory).iterdir()), [])


class IsolationPolicyTests(unittest.TestCase):
    def test_runtime_release_manifest_requires_exact_77_file_contract(self) -> None:
        source: dict[str, object] = {
            "commit": "a" * 40,
            "version": "0.1.0",
        }

        def release_manifest(file_count: int) -> dict[str, object]:
            return {
                "build": {},
                "commit": source["commit"],
                "files": [{} for _ in range(file_count)],
                "schema": "ascendany.release.v2",
                "sourceDateEpoch": 0,
                "version": source["version"],
            }

        attestation._validate_release_manifest_identity(release_manifest(77), source)
        for file_count in (76, 78):
            with self.subTest(file_count=file_count):
                with self.assertRaises(contract.TrainerRuntimeError):
                    attestation._validate_release_manifest_identity(
                        release_manifest(file_count), source
                    )

    def test_production_package_imports_only_compute_contract_modules(self) -> None:
        self.assertEqual(
            {path.name for path in PACKAGE_ROOT.glob("*.py")},
            {
                "__init__.py",
                "__main__.py",
                "attestation.py",
                "cli.py",
                "contract.py",
                "model.py",
                "train.py",
            },
        )
        allowed = {
            "__future__",
            "dataclasses",
            "datetime",
            "decimal",
            "hashlib",
            "importlib",
            "json",
            "math",
            "os",
            "pathlib",
            "re",
            "socket",
            "stat",
            "sys",
            "sysconfig",
            "torch",
            "typing",
            "urllib",
        }
        imported: set[str] = set()
        for source_path in PACKAGE_ROOT.glob("*.py"):
            tree = ast.parse(source_path.read_text(encoding="utf-8"), filename=str(source_path))
            for node in ast.walk(tree):
                if isinstance(node, ast.Import):
                    imported.update(alias.name.split(".", 1)[0] for alias in node.names)
                elif isinstance(node, ast.ImportFrom) and node.level == 0 and node.module:
                    imported.add(node.module.split(".", 1)[0])
        self.assertLessEqual(imported, allowed)

    def test_runtime_dependency_and_forbidden_capabilities_are_exact(self) -> None:
        configuration = tomllib.loads((TRAINER_ROOT / "pyproject.toml").read_text(encoding="utf-8"))
        self.assertEqual(
            configuration["project"]["dependencies"], ["numpy==2.4.4", "torch==2.13.0"]
        )
        production = "\n".join(
            path.read_text(encoding="utf-8") for path in PACKAGE_ROOT.glob("*.py")
        ).lower()
        for forbidden in (
            "psycopg",
            "sqlalchemy",
            "requests",
            "socket.socket(",
            "subprocess",
            "urlopen",
            "database_url",
        ):
            with self.subTest(forbidden=forbidden):
                self.assertNotIn(forbidden, production)

    def test_runtime_locks_are_hashed_and_target_exact_torch_wheels(self) -> None:
        cpu_lock = (TRAINER_ROOT / "test-requirements-cpu.lock").read_text(encoding="utf-8")
        cuda_lock = (TRAINER_ROOT / "runtime-requirements-cu130.lock").read_text(
            encoding="utf-8"
        )
        for lock in (cpu_lock, cuda_lock):
            self.assertIn("numpy==2.4.4", lock)
            self.assertNotIn("file:///", lock)
            self.assertNotIn("/tmp/", lock)
            self.assertIn("--hash=sha256:", lock)
        self.assertIn(
            "torch-2.13.0%2Bcpu-cp314-cp314-manylinux_2_28_x86_64.whl",
            cpu_lock,
        )
        self.assertIn(
            "--hash=sha256:d20fa53ee744502fa4c69818a720b05ca0d37abd055d4f6e66cae155114bc691",
            cpu_lock,
        )
        self.assertIn("torch==2.13.0+cu130", cuda_lock)
        self.assertIn(
            "--hash=sha256:e231302a457298d0236f7bde31082568f6cd0613b66b4eb46849e8ad53c2e38d",
            cuda_lock,
        )
        wheels = json.loads((TRAINER_ROOT / "runtime-wheels-cu130.json").read_bytes())
        self.assertEqual(wheels["schema"], "ascendany.trainer-runtime.wheels.v1")
        self.assertEqual(len(wheels["wheels"]), 30)
        torch_wheel = next(item for item in wheels["wheels"] if item["name"] == "torch")
        self.assertEqual(
            torch_wheel["filename"],
            "torch-2.13.0+cu130-cp314-cp314-manylinux_2_28_x86_64.whl",
        )
        self.assertEqual(
            torch_wheel["sha256"],
            "e231302a457298d0236f7bde31082568f6cd0613b66b4eb46849e8ad53c2e38d",
        )

    def test_runtime_wheel_contract_rejects_url_hash_filename_and_closure_drift(self) -> None:
        lock = (TRAINER_ROOT / "runtime-requirements-cu130.lock").read_bytes()
        closure = (TRAINER_ROOT / "runtime-closure-cu130.json").read_bytes()
        wheels = (TRAINER_ROOT / "runtime-wheels-cu130.json").read_bytes()
        attestation._validate_wheel_contract(lock, closure, wheels)

        wheel_document = json.loads(wheels)
        mutations: dict[str, tuple[bytes, bytes, bytes]] = {}
        for label, field, value in (
            ("url", "url", "http://download.pytorch.org/escape.whl"),
            ("hash", "sha256", "0" * 64),
            ("filename", "filename", "torch-escape.whl"),
        ):
            mutated = copy.deepcopy(wheel_document)
            mutated["wheels"][-1][field] = value
            encoded = json.dumps(mutated, sort_keys=True, separators=(",", ":")).encode() + b"\n"
            mutations[label] = (lock, closure, encoded)
        mutations["lock"] = (lock.replace(b"torch==2.13.0+cu130", b"torch==2.13.1+cu130"), closure, wheels)
        mutations["closure"] = (
            lock,
            closure.replace(b'"version":"2.13.0+cu130"', b'"version":"2.13.1+cu130"'),
            wheels,
        )
        missing = copy.deepcopy(wheel_document)
        missing["wheels"].pop()
        mutations["missing wheel"] = (
            lock,
            closure,
            json.dumps(missing, sort_keys=True, separators=(",", ":")).encode() + b"\n",
        )
        extra = copy.deepcopy(wheel_document)
        extra["wheels"].append(copy.deepcopy(extra["wheels"][-1]))
        mutations["extra wheel"] = (
            lock,
            closure,
            json.dumps(extra, sort_keys=True, separators=(",", ":")).encode() + b"\n",
        )
        for label, values in mutations.items():
            with self.subTest(label=label):
                with self.assertRaises(contract.TrainerRuntimeError):
                    attestation._validate_wheel_contract(*values)


if __name__ == "__main__":
    unittest.main()

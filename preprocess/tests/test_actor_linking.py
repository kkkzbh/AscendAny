from __future__ import annotations

from preprocess.config import MappingConfig
from preprocess.linking.actor_linker import CandidateIdentity, SubmissionActor, plan_submission_links


def test_link_actors_idempotent_updates() -> None:
    cfg = MappingConfig(
        primary_keys=["student_no", "name"],
        actor_sources=["pta_*_account"],
        strict_mode=True,
    )
    candidates = [
        CandidateIdentity(
            student_id=101,
            canonical_name="Alice",
            identity_source="pta_icpc_student_no",
            external_id="2023001",
            external_name="Alice",
        )
    ]
    submissions = [
        SubmissionActor(
            submission_id=1,
            actor_source="pta_icpc_account",
            actor_external_id="2023001",
            actor_name="Alice",
            student_id=None,
            raw={},
        )
    ]

    first = plan_submission_links(submissions=submissions, candidates=candidates, cfg=cfg)
    assert first.matched == 1
    assert first.updated == 1
    assert first.ambiguous == 0
    assert first.unmatched == 0
    assert first.updates[0].student_id == 101

    submissions[0].student_id = first.updates[0].student_id
    submissions[0].raw["linking"] = first.updates[0].link_payload
    second = plan_submission_links(submissions=submissions, candidates=candidates, cfg=cfg)
    assert second.matched == 1
    assert second.updated == 0
    assert second.ambiguous == 0
    assert second.unmatched == 0


def test_link_actors_ambiguous_name_keeps_student_null() -> None:
    cfg = MappingConfig(
        primary_keys=["name"],
        actor_sources=["datastructure_nickname"],
        strict_mode=True,
    )
    candidates = [
        CandidateIdentity(
            student_id=101,
            canonical_name="王伟",
            identity_source="datastructure_student_no",
            external_id="2023001",
            external_name="王伟",
        ),
        CandidateIdentity(
            student_id=102,
            canonical_name="王伟",
            identity_source="datastructure_student_no",
            external_id="2023002",
            external_name="王伟",
        ),
    ]
    submissions = [
        SubmissionActor(
            submission_id=1,
            actor_source="datastructure_nickname",
            actor_external_id="王伟",
            actor_name="王伟",
            student_id=None,
            raw={},
        )
    ]

    result = plan_submission_links(submissions=submissions, candidates=candidates, cfg=cfg)
    assert result.matched == 0
    assert result.ambiguous == 1
    assert result.unmatched == 0
    assert result.updated == 1
    assert result.updates[0].student_id is None
    assert result.updates[0].link_payload["status"] == "ambiguous"

from __future__ import annotations

import asyncio
from datetime import UTC, datetime, timedelta

import pytest

from apps.api.core.config import Settings
from apps.api.core.errors import AppError
from apps.api.core.security import hash_password
from apps.api.db.repository import AccountProfileRow, AccountRow, RefreshTokenRow
from apps.api.schemas.auth import (
    AuthProfileUpdateRequest,
    LoginRequest,
    RefreshRequest,
    RegisterRequest,
)
from apps.api.services.auth import AuthService


def _build_register_request(**overrides) -> RegisterRequest:
    payload = {
        "username": "alice_01",
        "password": "password_123",
        "studentId": "20230001",
        "ptaNickname": "Alice",
    }
    payload.update(overrides)
    return RegisterRequest(**payload)


async def _create_account(repo: FakeAuthRepo, *, username: str, password_hash: str) -> AccountRow:
    account = await repo.create_account(
        username=username,
        display_name=username,
        password_hash=password_hash,
    )
    assert account is not None
    return account


class FakeAuthRepo:
    def __init__(self) -> None:
        self._next_account_id = 1
        self._next_token_id = 1
        self._next_student_entity_id = 1
        self.accounts_by_id: dict[int, AccountRow] = {}
        self.account_by_username_norm: dict[str, int] = {}
        self.account_by_display_name_norm: dict[str, int] = {}
        self.profiles: dict[int, AccountProfileRow] = {}
        self.refresh_by_hash: dict[str, RefreshTokenRow] = {}
        self.contacts: set[tuple[str, str]] = set()
        self.student_entity_by_no: dict[str, int] = {}
        self.active_claims: dict[str, int] = {}
        self.student_identities: dict[tuple[str, str], int] = {}
        self.reassigned_calls: list[tuple[int, tuple[str, ...], str]] = []
        self.related_students_by_nickname: dict[str, set[int]] = {}
        self.achievement_merge_calls: list[tuple[int, tuple[int, ...]]] = []

    async def create_account(
        self, username: str, display_name: str, password_hash: str
    ) -> AccountRow | None:
        username_norm = username.strip().lower()
        display_name_norm = display_name.strip().lower()
        if (
            username_norm in self.account_by_username_norm
            or display_name_norm in self.account_by_display_name_norm
        ):
            return None
        account_id = self._next_account_id
        self._next_account_id += 1
        row = AccountRow(
            account_id=account_id,
            username=username.strip(),
            display_name=display_name.strip(),
            password_hash=password_hash,
            is_active=True,
        )
        self.accounts_by_id[account_id] = row
        self.account_by_username_norm[username_norm] = account_id
        self.account_by_display_name_norm[display_name_norm] = account_id
        return row

    async def add_account_contact(self, account_id: int, contact_type: str, value: str) -> bool:
        key = (contact_type, value.strip().lower())
        if key in self.contacts:
            return False
        self.contacts.add(key)
        return True

    async def fetch_account_by_username(self, username: str) -> AccountRow | None:
        account_id = self.account_by_username_norm.get(username.strip().lower())
        if account_id is None:
            return None
        return self.accounts_by_id.get(account_id)

    async def fetch_account_by_id(self, account_id: int) -> AccountRow | None:
        return self.accounts_by_id.get(account_id)

    async def update_account_display_name(
        self, account_id: int, display_name: str
    ) -> AccountRow | None:
        row = self.accounts_by_id.get(account_id)
        if row is None:
            return None
        next_display_name = display_name.strip()
        next_display_name_norm = next_display_name.lower()
        existing_account = self.account_by_display_name_norm.get(next_display_name_norm)
        if existing_account is not None and existing_account != account_id:
            return None
        old_display_name_norm = row.display_name.strip().lower()
        self.account_by_display_name_norm.pop(old_display_name_norm, None)
        updated = AccountRow(
            account_id=row.account_id,
            username=row.username,
            display_name=next_display_name,
            password_hash=row.password_hash,
            is_active=row.is_active,
            is_admin=row.is_admin,
        )
        self.accounts_by_id[account_id] = updated
        self.account_by_display_name_norm[next_display_name_norm] = account_id
        return updated

    async def touch_account_login(self, account_id: int) -> None:
        _ = account_id

    async def delete_account(self, account_id: int) -> None:
        row = self.accounts_by_id.pop(account_id, None)
        if row is None:
            return
        self.account_by_username_norm.pop(row.username.lower(), None)
        self.account_by_display_name_norm.pop(row.display_name.lower(), None)
        self.profiles.pop(account_id, None)

    async def fetch_account_profile(self, account_id: int) -> AccountProfileRow | None:
        return self.profiles.get(account_id)

    async def upsert_account_profile(
        self, account_id: int, student_id: str | None, pta_nickname: str | None
    ) -> AccountProfileRow:
        row = AccountProfileRow(
            student_id=student_id,
            pta_nickname=pta_nickname,
            updated_at=datetime.now(UTC),
        )
        self.profiles[account_id] = row
        return row

    async def ensure_student_by_student_no(
        self,
        student_no: str,
        student_name: str | None,
        identity_source: str = "user_profile_student_no",
    ) -> int:
        _ = student_name
        _ = identity_source
        normalized = student_no.strip()
        student_id = self.student_entity_by_no.get(normalized)
        if student_id is not None:
            return student_id
        student_id = self._next_student_entity_id
        self._next_student_entity_id += 1
        self.student_entity_by_no[normalized] = student_id
        return student_id

    async def claim_student_nickname(
        self,
        student_id: int,
        nickname: str,
        account_id: int | None = None,
    ) -> bool:
        _ = account_id
        normalized = nickname.strip().casefold()
        current = self.active_claims.get(normalized)
        if current is not None and current != student_id:
            return False
        # one active nickname per student
        for existing_nickname, existing_student_id in list(self.active_claims.items()):
            if existing_student_id == student_id and existing_nickname != normalized:
                del self.active_claims[existing_nickname]
        self.active_claims[normalized] = student_id
        return True

    async def upsert_student_identity(
        self,
        student_id: int,
        source: str,
        external_id: str,
        external_name: str | None,
    ) -> None:
        _ = external_name
        self.student_identities[(source, external_id.strip())] = student_id

    async def reassign_submissions_by_nicknames(
        self,
        student_id: int,
        nicknames: list[str],
        reason: str = "claim_backfill",
    ) -> int:
        normalized = tuple(sorted({item.strip().casefold() for item in nicknames if item.strip()}))
        self.reassigned_calls.append((student_id, normalized, reason))
        return 0

    async def find_student_ids_by_submission_nicknames(
        self,
        nicknames: list[str],
    ) -> list[int]:
        related: set[int] = set()
        for nickname in nicknames:
            key = nickname.strip().casefold()
            if not key:
                continue
            related.update(self.related_students_by_nickname.get(key, set()))
        return sorted(related)

    async def merge_achievement_states_for_student(
        self,
        target_student_id: int,
        source_student_ids: list[int],
    ) -> int:
        normalized_sources = tuple(
            sorted({int(student_id) for student_id in source_student_ids if int(student_id) > 0})
        )
        self.achievement_merge_calls.append((target_student_id, normalized_sources))
        return len(normalized_sources)

    async def insert_refresh_token(
        self,
        account_id: int,
        token_hash: str,
        expires_at: datetime,
        device_id: str | None,
    ) -> RefreshTokenRow:
        row = RefreshTokenRow(
            token_id=self._next_token_id,
            account_id=account_id,
            token_hash=token_hash,
            expires_at=expires_at,
            revoked_at=None,
            device_id=device_id,
        )
        self._next_token_id += 1
        self.refresh_by_hash[token_hash] = row
        return row

    async def fetch_refresh_token_by_hash(self, token_hash: str) -> RefreshTokenRow | None:
        return self.refresh_by_hash.get(token_hash)

    async def revoke_refresh_token_by_id(self, token_id: int) -> None:
        for key, row in list(self.refresh_by_hash.items()):
            if row.token_id != token_id:
                continue
            self.refresh_by_hash[key] = RefreshTokenRow(
                token_id=row.token_id,
                account_id=row.account_id,
                token_hash=row.token_hash,
                expires_at=row.expires_at,
                revoked_at=datetime.now(UTC),
                device_id=row.device_id,
            )
            return

    async def revoke_refresh_token_by_hash(self, account_id: int, token_hash: str) -> bool:
        row = self.refresh_by_hash.get(token_hash)
        if row is None or row.account_id != account_id or row.revoked_at is not None:
            return False
        self.refresh_by_hash[token_hash] = RefreshTokenRow(
            token_id=row.token_id,
            account_id=row.account_id,
            token_hash=row.token_hash,
            expires_at=row.expires_at,
            revoked_at=datetime.now(UTC),
            device_id=row.device_id,
        )
        return True

    async def revoke_all_refresh_tokens(self, account_id: int) -> int:
        count = 0
        for key, row in list(self.refresh_by_hash.items()):
            if row.account_id != account_id or row.revoked_at is not None:
                continue
            self.refresh_by_hash[key] = RefreshTokenRow(
                token_id=row.token_id,
                account_id=row.account_id,
                token_hash=row.token_hash,
                expires_at=row.expires_at,
                revoked_at=datetime.now(UTC),
                device_id=row.device_id,
            )
            count += 1
        return count


def test_auth_register_login_refresh_and_profile(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    register_result = asyncio.run(
        service.register(
            _build_register_request(deviceId="desktop-1")
        )
    )

    assert register_result.account.username == "alice_01"
    assert register_result.account.displayName.startswith("user_")
    assert register_result.account.studentId == "20230001"
    assert register_result.account.ptaNickname == "Alice"
    assert register_result.refreshToken

    current = service.authenticate_access_token(register_result.accessToken)
    me_result = asyncio.run(service.me(current))
    assert me_result.account.username == "alice_01"
    assert me_result.account.displayName == register_result.account.displayName
    assert me_result.account.studentId == "20230001"
    assert me_result.account.ptaNickname == "Alice"

    refresh_result = asyncio.run(
        service.refresh(
            RefreshRequest(
                refreshToken=register_result.refreshToken,
                deviceId="desktop-1",
            )
        )
    )
    assert refresh_result.refreshToken != register_result.refreshToken
    assert refresh_result.account.displayName == register_result.account.displayName
    assert refresh_result.account.studentId == "20230001"
    assert refresh_result.account.ptaNickname == "Alice"

    login_result = asyncio.run(
        service.login(LoginRequest(username="alice_01", password="password_123"))
    )
    assert login_result.account.accountId == register_result.account.accountId


def test_auth_register_merges_legacy_achievement_states(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    repo = FakeAuthRepo()
    repo.related_students_by_nickname["alice"] = {77}
    service = AuthService(settings=settings, repository=repo)

    register_result = asyncio.run(service.register(_build_register_request()))

    assert register_result.account.accountId == "1"
    assert repo.achievement_merge_calls == [(1, (1, 77))]


def test_auth_register_rejects_claimed_nickname(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    repo = FakeAuthRepo()
    repo.active_claims["alice"] = 999
    service = AuthService(settings=settings, repository=repo)

    with pytest.raises(AppError) as exc_info:
        asyncio.run(
            service.register(
                _build_register_request(
                    username="alice_02",
                    studentId="20230002",
                    ptaNickname="Alice",
                )
            )
        )

    assert exc_info.value.status_code == 409
    assert exc_info.value.code == "AUTH_PTA_NICKNAME_TAKEN"


def test_auth_update_profile_updates_nickname(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    register_result = asyncio.run(service.register(_build_register_request()))
    current = service.authenticate_access_token(register_result.accessToken)

    profile = asyncio.run(
        service.update_profile(
            current,
            AuthProfileUpdateRequest(ptaNickname="Bob"),
        )
    )

    assert profile.studentId == "20230001"
    assert profile.ptaNickname == "Bob"


def test_auth_update_profile_updates_display_name(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    register_result = asyncio.run(service.register(_build_register_request()))
    current = service.authenticate_access_token(register_result.accessToken)

    profile = asyncio.run(
        service.update_profile(
            current,
            AuthProfileUpdateRequest(displayName="learner_88"),
        )
    )

    assert profile.displayName == "learner_88"
    assert profile.studentId == "20230001"
    assert profile.ptaNickname == "Alice"


def test_auth_update_profile_rejects_duplicate_display_name(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    first_register = asyncio.run(service.register(_build_register_request(username="alice_01")))
    second_register = asyncio.run(
        service.register(
            _build_register_request(
                username="alice_02",
                studentId="20230002",
                ptaNickname="Alice2",
            )
        )
    )

    first_current = service.authenticate_access_token(first_register.accessToken)
    second_current = service.authenticate_access_token(second_register.accessToken)

    asyncio.run(
        service.update_profile(
            first_current,
            AuthProfileUpdateRequest(displayName="same_name"),
        )
    )

    with pytest.raises(AppError) as exc_info:
        asyncio.run(
            service.update_profile(
                second_current,
                AuthProfileUpdateRequest(displayName="same_name"),
            )
        )
    assert exc_info.value.status_code == 409
    assert exc_info.value.code == "AUTH_DISPLAY_NAME_TAKEN"


def test_auth_update_profile_rejects_student_id_change(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    register_result = asyncio.run(service.register(_build_register_request()))
    current = service.authenticate_access_token(register_result.accessToken)

    with pytest.raises(AppError) as exc_info:
        asyncio.run(
            service.update_profile(
                current,
                AuthProfileUpdateRequest(studentId="20239999", ptaNickname="Bob"),
            )
        )

    assert exc_info.value.status_code == 403
    assert exc_info.value.code == "AUTH_STUDENT_ID_IMMUTABLE"


def test_auth_login_rejects_wrong_password(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    asyncio.run(service.register(_build_register_request()))

    with pytest.raises(AppError) as exc_info:
        asyncio.run(
            service.login(LoginRequest(username="alice_01", password="wrong-password"))
        )

    assert exc_info.value.status_code == 401
    assert exc_info.value.code == "AUTH_INVALID_CREDENTIALS"


def test_auth_login_accepts_stored_password_value_for_external_provider(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    settings.auth.provider = "app01_mysql"
    settings.auth.allow_stored_password_direct_login = True
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    account = asyncio.run(
        _create_account(repo, username="alice_01", password_hash="pbkdf2_sha256$stored-value")
    )

    result = asyncio.run(
        service.login(
            LoginRequest(
                username="alice_01",
                password="pbkdf2_sha256$stored-value",
                passwordMode="stored_value",
            )
        )
    )

    assert result.account.accountId == str(account.account_id)
    assert result.account.username == "alice_01"


def test_auth_login_rejects_wrong_stored_password_value(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    settings.auth.provider = "app01_mysql"
    settings.auth.allow_stored_password_direct_login = True
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    asyncio.run(
        _create_account(repo, username="alice_01", password_hash="pbkdf2_sha256$stored-value")
    )

    with pytest.raises(AppError) as exc_info:
        asyncio.run(
            service.login(
                LoginRequest(
                    username="alice_01",
                    password="pbkdf2_sha256$different",
                    passwordMode="stored_value",
                )
            )
        )

    assert exc_info.value.status_code == 401
    assert exc_info.value.code == "AUTH_INVALID_CREDENTIALS"


def test_auth_login_rejects_stored_password_value_when_flag_disabled(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    settings.auth.provider = "app01_mysql"
    settings.auth.allow_stored_password_direct_login = False
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    asyncio.run(
        _create_account(repo, username="alice_01", password_hash="pbkdf2_sha256$stored-value")
    )

    with pytest.raises(AppError) as exc_info:
        asyncio.run(
            service.login(
                LoginRequest(
                    username="alice_01",
                    password="pbkdf2_sha256$stored-value",
                    passwordMode="stored_value",
                )
            )
        )

    assert exc_info.value.status_code == 401
    assert exc_info.value.code == "AUTH_INVALID_CREDENTIALS"


def test_auth_login_rejects_stored_password_value_for_internal_provider(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    settings.auth.provider = "internal"
    settings.auth.allow_stored_password_direct_login = True
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    asyncio.run(
        _create_account(repo, username="alice_01", password_hash="pbkdf2_sha256$stored-value")
    )

    with pytest.raises(AppError) as exc_info:
        asyncio.run(
            service.login(
                LoginRequest(
                    username="alice_01",
                    password="pbkdf2_sha256$stored-value",
                    passwordMode="stored_value",
                )
            )
        )

    assert exc_info.value.status_code == 401
    assert exc_info.value.code == "AUTH_INVALID_CREDENTIALS"


def test_auth_login_plain_mode_still_verifies_password(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    settings.auth.provider = "app01_mysql"
    settings.auth.allow_stored_password_direct_login = True
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    password_hash = hash_password("password_123")
    account = asyncio.run(
        _create_account(repo, username="alice_01", password_hash=password_hash)
    )

    result = asyncio.run(
        service.login(
            LoginRequest(
                username="alice_01",
                password="password_123",
                passwordMode="plain",
            )
        )
    )

    assert result.account.accountId == str(account.account_id)


def test_auth_signup_policy_requires_contact(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    settings.auth.signup_policy = "require_phone_or_email"
    service = AuthService(settings=settings, repository=FakeAuthRepo())

    with pytest.raises(AppError) as exc_info:
        asyncio.run(service.register(_build_register_request()))

    assert exc_info.value.status_code == 400
    assert exc_info.value.code == "AUTH_SIGNUP_POLICY_VIOLATION"


def test_auth_policy_flags_for_or_policy(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    settings.auth.signup_policy = "require_phone_or_email"
    service = AuthService(settings=settings, repository=FakeAuthRepo())

    policy = service.get_policy()

    assert policy.signupPolicy == "require_phone_or_email"
    assert policy.requirePhone is False
    assert policy.requireEmail is False


def test_auth_refresh_rejects_expired_token(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    repo = FakeAuthRepo()
    service = AuthService(settings=settings, repository=repo)

    register_result = asyncio.run(service.register(_build_register_request()))

    token_hash = next(iter(repo.refresh_by_hash.keys()))
    row = repo.refresh_by_hash[token_hash]
    repo.refresh_by_hash[token_hash] = RefreshTokenRow(
        token_id=row.token_id,
        account_id=row.account_id,
        token_hash=row.token_hash,
        expires_at=datetime.now(UTC) - timedelta(minutes=1),
        revoked_at=None,
        device_id=row.device_id,
    )

    with pytest.raises(AppError) as exc_info:
        asyncio.run(service.refresh(RefreshRequest(refreshToken=register_result.refreshToken)))

    assert exc_info.value.status_code == 401
    assert exc_info.value.code == "AUTH_TOKEN_EXPIRED"


def test_auth_register_requires_student_id_and_pta_nickname(monkeypatch) -> None:
    monkeypatch.setenv("ASCENDANY_AUTH_JWT_SECRET", "test-secret")
    settings = Settings()
    service = AuthService(settings=settings, repository=FakeAuthRepo())

    with pytest.raises(AppError) as missing_student_exc:
        asyncio.run(
            service.register(
                _build_register_request(
                    username="alice_02",
                    studentId="   ",
                )
            )
        )
    assert missing_student_exc.value.status_code == 400
    assert missing_student_exc.value.code == "AUTH_STUDENT_ID_REQUIRED"

    with pytest.raises(AppError) as missing_nickname_exc:
        asyncio.run(
            service.register(
                _build_register_request(
                    username="alice_03",
                    ptaNickname="  ",
                )
            )
        )
    assert missing_nickname_exc.value.status_code == 400
    assert missing_nickname_exc.value.code == "AUTH_PTA_NICKNAME_REQUIRED"

from __future__ import annotations

import asyncio
from datetime import UTC, datetime, timedelta

import pytest

from apps.api.core.config import Settings
from apps.api.core.errors import AppError
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


class FakeAuthRepo:
    def __init__(self) -> None:
        self._next_account_id = 1
        self._next_token_id = 1
        self.accounts_by_id: dict[int, AccountRow] = {}
        self.account_by_username_norm: dict[str, int] = {}
        self.profiles: dict[int, AccountProfileRow] = {}
        self.refresh_by_hash: dict[str, RefreshTokenRow] = {}
        self.contacts: set[tuple[str, str]] = set()

    async def create_account(self, username: str, password_hash: str) -> AccountRow | None:
        username_norm = username.strip().lower()
        if username_norm in self.account_by_username_norm:
            return None
        account_id = self._next_account_id
        self._next_account_id += 1
        row = AccountRow(
            account_id=account_id,
            username=username.strip(),
            password_hash=password_hash,
            is_active=True,
        )
        self.accounts_by_id[account_id] = row
        self.account_by_username_norm[username_norm] = account_id
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

    async def touch_account_login(self, account_id: int) -> None:
        _ = account_id

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
    assert register_result.account.studentId == "20230001"
    assert register_result.account.ptaNickname == "Alice"
    assert register_result.refreshToken

    current = service.authenticate_access_token(register_result.accessToken)
    me_result = asyncio.run(service.me(current))
    assert me_result.account.username == "alice_01"
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
    assert refresh_result.account.studentId == "20230001"
    assert refresh_result.account.ptaNickname == "Alice"

    login_result = asyncio.run(
        service.login(LoginRequest(username="alice_01", password="password_123"))
    )
    assert login_result.account.accountId == register_result.account.accountId


def test_auth_update_profile_is_forbidden(monkeypatch) -> None:
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
    assert exc_info.value.code == "AUTH_PROFILE_IMMUTABLE"


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

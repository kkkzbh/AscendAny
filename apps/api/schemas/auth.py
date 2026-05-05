from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


SignupPolicy = Literal[
    "username_password_only",
    "require_phone_or_email",
    "require_phone_and_email",
]
ProvisionSource = Literal["local", "external_sso"]


class RegisterRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    username: str = Field(min_length=3, max_length=32)
    password: str = Field(min_length=8, max_length=128)
    studentId: str = Field(min_length=1, max_length=64)
    ptaNickname: str = Field(min_length=1, max_length=128)
    phone: str | None = Field(default=None, max_length=32)
    email: str | None = Field(default=None, max_length=320)
    deviceId: str | None = Field(default=None, max_length=128)


class LoginRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    username: str = Field(min_length=1, max_length=64)
    password: str = Field(min_length=1, max_length=128)
    passwordMode: Literal["plain", "stored_value"] = "plain"
    deviceId: str | None = Field(default=None, max_length=128)


class RefreshRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    refreshToken: str = Field(min_length=16, max_length=512)
    deviceId: str | None = Field(default=None, max_length=128)


class LogoutRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    refreshToken: str | None = Field(default=None, max_length=512)


class SSOExchangeRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    token: str = Field(min_length=32, max_length=4096)


class LocalPasswordBootstrapRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    newPassword: str = Field(min_length=8, max_length=128)


class AuthPolicyResponse(BaseModel):
    signupPolicy: SignupPolicy
    requirePhone: bool
    requireEmail: bool


class AuthAccountResponse(BaseModel):
    accountId: str
    username: str
    displayName: str
    isAdmin: bool = False
    studentId: str | None = None
    ptaNickname: str | None = None
    provisionSource: ProvisionSource = "local"
    localPasswordEnabled: bool = True


class AuthTokensResponse(BaseModel):
    accessToken: str
    accessTokenExpiresAt: datetime
    refreshToken: str
    refreshTokenExpiresAt: datetime
    account: AuthAccountResponse


class AuthMeResponse(BaseModel):
    account: AuthAccountResponse


class AuthProfileUpdateRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    displayName: str | None = Field(default=None, max_length=128)
    studentId: str | None = Field(default=None, max_length=64)
    ptaNickname: str | None = Field(default=None, max_length=128)


class AuthProfileResponse(BaseModel):
    displayName: str | None = None
    studentId: str | None = None
    ptaNickname: str | None = None


class LocalPasswordBootstrapResponse(BaseModel):
    ok: bool

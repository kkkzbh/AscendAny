from __future__ import annotations

import base64
import binascii
import hashlib
import hmac
import json
import secrets
import time
from datetime import UTC, datetime, timedelta
from typing import Any


def _b64url_encode(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def _b64url_decode(value: str) -> bytes:
    padding = "=" * ((4 - len(value) % 4) % 4)
    return base64.urlsafe_b64decode((value + padding).encode("ascii"))


def hash_password(password: str, pepper: str = "") -> str:
    if not password:
        raise ValueError("password cannot be empty")
    salt = secrets.token_bytes(16)
    dk = hashlib.scrypt(
        (password + pepper).encode("utf-8"),
        salt=salt,
        n=2**14,
        r=8,
        p=1,
        dklen=32,
    )
    return "scrypt$16384$8$1$%s$%s" % (
        _b64url_encode(salt),
        _b64url_encode(dk),
    )


def verify_password(password: str, encoded: str, pepper: str = "") -> bool:
    if not encoded:
        return False

    if encoded.startswith("scrypt$"):
        try:
            algorithm, n_raw, r_raw, p_raw, salt_raw, digest_raw = encoded.split("$", 5)
            if algorithm != "scrypt":
                return False
            n = int(n_raw)
            r = int(r_raw)
            p = int(p_raw)
            salt = _b64url_decode(salt_raw)
            expected = _b64url_decode(digest_raw)
        except (ValueError, TypeError, binascii.Error, AttributeError):
            return False

        actual = hashlib.scrypt(
            (password + pepper).encode("utf-8"),
            salt=salt,
            n=n,
            r=r,
            p=p,
            dklen=len(expected),
        )
        return hmac.compare_digest(actual, expected)

    # Django-style hash: pbkdf2_sha256$<iterations>$<salt>$<digest>
    if encoded.startswith("pbkdf2_sha256$"):
        try:
            algorithm, iter_raw, salt, digest = encoded.split("$", 3)
            if algorithm != "pbkdf2_sha256":
                return False
            iterations = int(iter_raw)
            if iterations <= 0:
                return False
            if not salt:
                return False
        except (ValueError, TypeError, AttributeError):
            return False

        # NOTE: This verifier intentionally ignores `pepper`.
        dk = hashlib.pbkdf2_hmac(
            "sha256",
            password.encode("utf-8"),
            salt.encode("utf-8"),
            iterations,
            dklen=32,
        )
        actual = base64.b64encode(dk).decode("ascii").strip()
        return hmac.compare_digest(actual, str(digest))

    return False


def hash_refresh_token(token: str, pepper: str = "") -> str:
    return hashlib.sha256((pepper + token).encode("utf-8")).hexdigest()


def generate_refresh_token() -> str:
    return secrets.token_urlsafe(48)


def sign_access_token(
    payload: dict[str, Any], secret: str, expires_in_seconds: int
) -> tuple[str, datetime]:
    now = int(time.time())
    exp = now + max(1, expires_in_seconds)
    full_payload = dict(payload)
    full_payload["iat"] = now
    full_payload["exp"] = exp

    header = {"alg": "HS256", "typ": "JWT"}
    header_raw = _b64url_encode(
        json.dumps(header, separators=(",", ":"), sort_keys=True).encode("utf-8")
    )
    payload_raw = _b64url_encode(
        json.dumps(full_payload, separators=(",", ":"), sort_keys=True).encode("utf-8")
    )
    signing_input = f"{header_raw}.{payload_raw}".encode("ascii")
    signature = hmac.new(secret.encode("utf-8"), signing_input, hashlib.sha256).digest()
    token = f"{header_raw}.{payload_raw}.{_b64url_encode(signature)}"
    return token, datetime.now(UTC) + timedelta(seconds=max(1, expires_in_seconds))


def verify_access_token(token: str, secret: str) -> dict[str, Any]:
    parts = token.split(".")
    if len(parts) != 3:
        raise ValueError("invalid_token_format")

    header_raw, payload_raw, signature_raw = parts
    signing_input = f"{header_raw}.{payload_raw}".encode("ascii")
    expected_sig = hmac.new(
        secret.encode("utf-8"), signing_input, hashlib.sha256
    ).digest()
    try:
        actual_sig = _b64url_decode(signature_raw)
    except (ValueError, binascii.Error) as exc:
        raise ValueError("invalid_token_signature") from exc

    if not hmac.compare_digest(actual_sig, expected_sig):
        raise ValueError("invalid_token_signature")

    try:
        payload = json.loads(_b64url_decode(payload_raw).decode("utf-8"))
    except (ValueError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError("invalid_token_payload") from exc

    if not isinstance(payload, dict):
        raise ValueError("invalid_token_payload")

    exp = payload.get("exp")
    if not isinstance(exp, int):
        raise ValueError("invalid_token_exp")
    if exp < int(time.time()):
        raise ValueError("token_expired")

    return payload

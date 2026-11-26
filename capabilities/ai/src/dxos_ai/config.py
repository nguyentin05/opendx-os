# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from functools import lru_cache

from pydantic import AnyHttpUrl, PostgresDsn, SecretStr, TypeAdapter, field_validator
from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    database_url: SecretStr
    oidc_issuer_url: str = ""
    oidc_jwks_url: AnyHttpUrl | None = None
    oidc_audience: str = "dxos-runtime"
    ai_model_master_key: SecretStr | None = None
    ai_hosted_model_endpoints: set[str] = set()
    ai_local_model_endpoints: dict[str, list[str]] = {}

    @field_validator("database_url")
    @classmethod
    def validate_database_url(cls, value: SecretStr) -> SecretStr:
        try:
            TypeAdapter(PostgresDsn).validate_python(value.get_secret_value())
        except ValueError:
            raise ValueError("invalid PostgreSQL database URL") from None
        return value


@lru_cache
def settings() -> Settings:
    return Settings()

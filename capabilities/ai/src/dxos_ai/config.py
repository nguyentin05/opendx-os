# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from functools import lru_cache

from pydantic import PostgresDsn, SecretStr, TypeAdapter, field_validator
from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    database_url: SecretStr

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

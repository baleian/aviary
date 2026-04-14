"""User admin — Keycloak-backed create/delete + per-user Vault credentials.

The DB row is a lazy mirror of the Keycloak user, created by the API's OIDC
sign-in handler. Admin "deletes" here remove from both Keycloak and the DB
(which cascades any owned artefacts).
"""

from __future__ import annotations

import uuid

import httpx
from fastapi import APIRouter, Form, HTTPException, Request
from fastapi.responses import HTMLResponse, RedirectResponse
from sqlalchemy import select

from aviary_shared.db.models import User

from app.clients.keycloak import client as keycloak
from app.clients.vault import vault
from app.db import async_session_factory
from app.templates import templates

router = APIRouter()


@router.get("/users", response_class=HTMLResponse)
async def list_users(request: Request):
    try:
        kc_users = await keycloak.list_users()
    except httpx.HTTPError as exc:
        raise HTTPException(502, f"Keycloak unreachable: {exc}") from exc

    async with async_session_factory() as db:
        db_rows = {
            u.external_id: u for u in (await db.execute(select(User))).scalars().all()
        }

    rows = []
    for kc in kc_users:
        sub = kc["id"]
        u = db_rows.get(sub)
        rows.append({
            "keycloak_id": sub,
            "db_id": str(u.id) if u else None,
            "username": kc.get("username"),
            "email": kc.get("email"),
            "display_name": kc.get("firstName", "") + (" " + kc.get("lastName", "") if kc.get("lastName") else ""),
            "enabled": kc.get("enabled", False),
            "has_db_row": u is not None,
        })
    return templates.TemplateResponse(request, "users/list.html", {"users": rows})


@router.get("/users/new", response_class=HTMLResponse)
async def new_user_form(request: Request):
    return templates.TemplateResponse(request, "users/new.html", {})


@router.post("/users")
async def create_user(
    username: str = Form(...),
    email: str = Form(...),
    display_name: str = Form(...),
    password: str = Form(...),
):
    try:
        await keycloak.create_user(
            username=username, email=email,
            display_name=display_name, password=password,
        )
    except httpx.HTTPStatusError as exc:
        detail = exc.response.text if exc.response is not None else str(exc)
        raise HTTPException(400, f"Keycloak rejected: {detail}") from exc
    except httpx.HTTPError as exc:
        raise HTTPException(502, f"Keycloak unreachable: {exc}") from exc
    return RedirectResponse("/users", status_code=303)


@router.get("/users/{keycloak_id}", response_class=HTMLResponse)
async def user_detail(request: Request, keycloak_id: str):
    async with async_session_factory() as db:
        user = (
            await db.execute(select(User).where(User.external_id == keycloak_id))
        ).scalar_one_or_none()

    credentials: list[str] = []
    if user is not None:
        try:
            credentials = await vault().list_user_credentials(user.external_id)
        except httpx.HTTPError:
            credentials = []

    return templates.TemplateResponse(request, "users/detail.html", {
        "keycloak_id": keycloak_id,
        "user": user,
        "credentials": credentials,
    })


@router.post("/users/{keycloak_id}/delete")
async def delete_user(keycloak_id: str):
    try:
        await keycloak.delete_user(keycloak_id)
    except httpx.HTTPError:
        pass  # Best-effort — still drop the DB row below.
    async with async_session_factory() as db:
        user = (
            await db.execute(select(User).where(User.external_id == keycloak_id))
        ).scalar_one_or_none()
        if user is not None:
            await db.delete(user)
            await db.commit()
    return RedirectResponse("/users", status_code=303)


@router.post("/users/{keycloak_id}/credentials")
async def put_credential(
    keycloak_id: str,
    name: str = Form(...),
    value: str = Form(...),
):
    async with async_session_factory() as db:
        user = (
            await db.execute(select(User).where(User.external_id == keycloak_id))
        ).scalar_one_or_none()
    if user is None:
        raise HTTPException(404, "User has not signed in yet — cannot set credentials")
    await vault().write_user_credential(user.external_id, name, value)
    return RedirectResponse(f"/users/{keycloak_id}", status_code=303)


@router.post("/users/{keycloak_id}/credentials/{name}/delete")
async def delete_credential(keycloak_id: str, name: str):
    async with async_session_factory() as db:
        user = (
            await db.execute(select(User).where(User.external_id == keycloak_id))
        ).scalar_one_or_none()
    if user is None:
        raise HTTPException(404, "User not found")
    await vault().delete_user_credential(user.external_id, name)
    return RedirectResponse(f"/users/{keycloak_id}", status_code=303)

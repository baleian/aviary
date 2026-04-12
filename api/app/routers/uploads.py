"""File upload endpoints — store and serve user-uploaded images."""

import uuid

from fastapi import APIRouter, Depends, HTTPException, UploadFile, status
from fastapi.responses import Response
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.dependencies import get_current_user
from app.db.models import User
from app.db.session import get_db

from aviary_shared.db.models import FileUpload

router = APIRouter()

ALLOWED_CONTENT_TYPES = {"image/png", "image/jpeg", "image/gif", "image/webp"}
MAX_FILE_SIZE = 5 * 1024 * 1024  # 5 MB


@router.post("")
async def upload_file(
    file: UploadFile,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    if file.content_type not in ALLOWED_CONTENT_TYPES:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail=f"Unsupported file type: {file.content_type}. Allowed: {', '.join(sorted(ALLOWED_CONTENT_TYPES))}",
        )

    data = await file.read()
    if len(data) > MAX_FILE_SIZE:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail=f"File too large ({len(data)} bytes). Maximum: {MAX_FILE_SIZE} bytes",
        )

    upload = FileUpload(
        user_id=user.id,
        content_type=file.content_type,
        filename=file.filename or "untitled",
        data=data,
        size_bytes=len(data),
    )
    db.add(upload)
    await db.flush()
    file_id = str(upload.id)
    await db.commit()

    return {
        "file_id": file_id,
        "filename": upload.filename,
        "content_type": upload.content_type,
        "size_bytes": upload.size_bytes,
    }


@router.get("/{file_id}")
async def get_file(
    file_id: uuid.UUID,
    user: User = Depends(get_current_user),
    db: AsyncSession = Depends(get_db),
):
    result = await db.execute(select(FileUpload).where(FileUpload.id == file_id))
    upload = result.scalar_one_or_none()
    if upload is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="File not found")

    return Response(
        content=upload.data,
        media_type=upload.content_type,
        headers={"Cache-Control": "private, max-age=86400, immutable"},
    )

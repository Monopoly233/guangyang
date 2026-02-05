from fastapi import APIRouter, UploadFile, File, HTTPException
from fastapi.responses import StreamingResponse
from typing import List, Optional
import pandas as pd
import io
import logging
import traceback
from urllib.parse import quote

from .utils import guess_primary_key_column, prepare_table_data, compare_excel_tables
import io
import pandas as pd

logger = logging.getLogger(__name__)

router = APIRouter(prefix="", tags=["compare"])


@router.post("/compare/")
async def compare_excel(file1: UploadFile = File(...), file2: UploadFile = File(...)):
    try:
        if file1 is None or file2 is None:
            raise HTTPException(status_code=400, detail='请上传两个Excel文件')

        content1 = await file1.read()
        content2 = await file2.read()

        df1 = pd.read_excel(io.BytesIO(content1))
        df2 = pd.read_excel(io.BytesIO(content2))

        onlyid = guess_primary_key_column(df1)
        if not onlyid:
            raise HTTPException(status_code=400, detail='无法猜测主键列，请确保包含明显的编号列')

        if onlyid not in df1.columns or onlyid not in df2.columns:
            raise HTTPException(status_code=400, detail=f'Excel文件中必须同时包含"{onlyid}"列')

        try:
            serials1 = set(df1[onlyid].astype(str).dropna())
            serials2 = set(df2[onlyid].astype(str).dropna())
        except Exception as e:
            raise HTTPException(status_code=400, detail=f'处理序列号时出错: {str(e)}')

        reduced_tbl, increased_tbl, different_tbl = compare_excel_tables(
            df1, df2, onlyid, file1.filename, file2.filename
        )

        response_data = {
            'reduced': reduced_tbl,
            'increased': increased_tbl,
            'different': different_tbl,
        }

        if not any(response_data.values()):
            return {'error': '没有找到任何差异数据'}

        return response_data
    except HTTPException:
        raise
    except Exception:
        logger.error(f"请求处理出错: {traceback.format_exc()}")
        raise HTTPException(status_code=500, detail='服务器内部错误')

@router.post("/compare/export")
async def compare_excel_export(file1: UploadFile = File(...), file2: UploadFile = File(...)):
    try:
        if file1 is None or file2 is None:
            raise HTTPException(status_code=400, detail='请上传两个Excel文件')

        content1 = await file1.read()
        content2 = await file2.read()

        df1 = pd.read_excel(io.BytesIO(content1))
        df2 = pd.read_excel(io.BytesIO(content2))

        onlyid = guess_primary_key_column(df1)
        if not onlyid:
            raise HTTPException(status_code=400, detail='无法猜测主键列，请确保包含明显的编号列')
        if onlyid not in df1.columns or onlyid not in df2.columns:
            raise HTTPException(status_code=400, detail=f'Excel文件中必须同时包含"{onlyid}"列')

        reduced_tbl, increased_tbl, different_tbl = compare_excel_tables(
            df1, df2, onlyid, file1.filename, file2.filename
        )

        # 将表格写入 xlsx（每个结果一个工作表）
        output = io.BytesIO()
        with pd.ExcelWriter(output, engine="openpyxl") as writer:
            def write_sheet(tbl, name):
                if tbl and isinstance(tbl, list) and len(tbl) > 1:
                    headers = tbl[0]
                    rows = tbl[1:]
                    df = pd.DataFrame(rows, columns=headers)
                    df.to_excel(writer, sheet_name=name, index=False)

            write_sheet(reduced_tbl, "减少项")
            write_sheet(increased_tbl, "增加项")
            write_sheet(different_tbl, "差异项")

            # 如果没有任何差异数据，为避免空工作簿报错，输出一张提示表
            has_data = any(
                tbl and isinstance(tbl, list) and len(tbl) > 1
                for tbl in (reduced_tbl, increased_tbl, different_tbl)
            )
            if not has_data:
                pd.DataFrame([{"结果": "未发现差异", "提示": "两份文件内容一致"}]).to_excel(
                    writer, sheet_name="结果", index=False
                )
        output.seek(0)

        filename = f"对比结果_{file1.filename}_vs_{file2.filename}.xlsx"
        encoded_name = quote(filename)
        return StreamingResponse(
            output,
            media_type="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
            headers={
                # 同时提供 ASCII 回退与 RFC 5987 编码，避免非 ASCII 触发 latin-1 编码报错
                "Content-Disposition": f"attachment; filename=\"comparison_result.xlsx\"; filename*=UTF-8''{encoded_name}",
            },
        )
    except HTTPException:
        raise
    except Exception:
        logger.error(f"导出对比Excel出错: {traceback.format_exc()}")
        raise HTTPException(status_code=500, detail='服务器内部错误')
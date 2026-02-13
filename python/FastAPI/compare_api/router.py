from fastapi import APIRouter, UploadFile, File, HTTPException
from fastapi.responses import StreamingResponse
from typing import List, Optional
import pandas as pd
import io
import logging
import traceback
from urllib.parse import quote

from .utils import guess_primary_key_column, prepare_table_data, compare_excel_tables, compare_excel_tables_df, read_excel_bytes
import io
import pandas as pd
from openpyxl import Workbook
from openpyxl.utils.dataframe import dataframe_to_rows

logger = logging.getLogger(__name__)

router = APIRouter(prefix="", tags=["compare"])


@router.post("/compare/")
async def compare_excel(file1: UploadFile = File(...), file2: UploadFile = File(...)):
    try:
        if file1 is None or file2 is None:
            raise HTTPException(status_code=400, detail='请上传两个Excel文件')

        content1 = await file1.read()
        content2 = await file2.read()

        try:
            df1 = read_excel_bytes(content1, file1.filename or "")
            df2 = read_excel_bytes(content2, file2.filename or "")
        except Exception as e:
            raise HTTPException(status_code=400, detail=f"读取Excel失败: {str(e)}")

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

        try:
            df1 = read_excel_bytes(content1, file1.filename or "")
            df2 = read_excel_bytes(content2, file2.filename or "")
        except Exception as e:
            raise HTTPException(status_code=400, detail=f"读取Excel失败: {str(e)}")

        onlyid = guess_primary_key_column(df1)
        if not onlyid:
            raise HTTPException(status_code=400, detail='无法猜测主键列，请确保包含明显的编号列')
        if onlyid not in df1.columns or onlyid not in df2.columns:
            raise HTTPException(status_code=400, detail=f'Excel文件中必须同时包含"{onlyid}"列')

        reduced_df, increased_df, different_df = compare_excel_tables_df(
            df1=df1,
            df2=df2,
            key=onlyid,
            file1name=file1.filename,
            file2name=file2.filename,
        )

        # 将结果写入 xlsx：固定三张表（增加项/减少项/变动项目）
        output = io.BytesIO()
        wb = Workbook()
        ws_inc = wb.active
        ws_inc.title = "增加项"
        ws_red = wb.create_sheet("减少项")
        ws_diff = wb.create_sheet("变动项目")

        def write_df(ws, df: Optional[pd.DataFrame], empty_msg: str):
            if df is None or df.empty:
                ws.append([empty_msg])
                return
            # openpyxl 不能直接写入 list/dict 等复杂对象（例如 “不同列” 是 list）
            dfw = df.copy()
            for c in dfw.columns:
                dfw[c] = dfw[c].map(
                    lambda x: ",".join(map(str, x)) if isinstance(x, (list, tuple, set)) else x
                )

            for r in dataframe_to_rows(dfw, index=False, header=True):
                ws.append(r)

        write_df(ws_inc, increased_df, "无增加项")
        write_df(ws_red, reduced_df, "无减少项")
        write_df(ws_diff, different_df, "无变动项目")

        wb.save(output)
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
from typing import Dict, Any
import pandas as pd
import io
import os
import logging
import traceback


logger = logging.getLogger(__name__)


def _read_workbook(content: bytes, filename: str) -> Dict[str, pd.DataFrame]:
    ext = os.path.splitext(filename or "")[1].lower()
    engine = None
    if ext == ".xls":
        engine = "xlrd"
    elif ext == ".xlsx":
        engine = "openpyxl"
    # 让 pandas 自行推断作为兜底
    try:
        return pd.read_excel(io.BytesIO(content), sheet_name=None, engine=engine)
    except Exception:
        # 再尝试不指定引擎
        logger.warning(f"读取 {filename} 失败，尝试不指定引擎：{traceback.format_exc()}")
        try:
            return pd.read_excel(io.BytesIO(content), sheet_name=None)
        except Exception:
            # 兜底：按照空工作簿返回，避免接口 500
            logger.error(f"读取 {filename} 仍失败，返回空工作簿：{traceback.format_exc()}")
            return {}


def _estimate_sheet(df: pd.DataFrame, sheet_name: str) -> Dict[str, Any]:
    try:
        # 丢弃全空行
        df_clean = df.dropna(how="all")
        rows = int(df_clean.shape[0])
        cols = int(df_clean.shape[1])

        # 统计非空单元格数量
        non_empty_cells = int(df_clean.notna().sum().sum())

        # 估算 JSON 体积：优先使用 to_json，其次退化为字符串化
        estimated_bytes = 0
        try:
            json_bytes = df_clean.to_json(orient="records", force_ascii=False).encode("utf-8", errors="ignore")
            estimated_bytes = len(json_bytes)
        except Exception:
            logger.warning(f"to_json 失败，转字符串估算，sheet={sheet_name}")
            json_bytes = df_clean.astype(str).to_json(orient="records", force_ascii=False).encode("utf-8", errors="ignore")
            estimated_bytes = len(json_bytes)

        return {
            "sheet_name": sheet_name,
            "rows": rows,
            "cols": cols,
            "non_empty_cells": non_empty_cells,
            "estimated_bytes": int(estimated_bytes),
        }
    except Exception:
        logger.error(f"估算工作表失败: {traceback.format_exc()}")
        return {
            "sheet_name": sheet_name,
            "rows": 0,
            "cols": 0,
            "non_empty_cells": 0,
            "estimated_bytes": 0,
        }


def estimate_excel_file(content: bytes, filename: str) -> Dict[str, Any]:
    try:
        wb = _read_workbook(content, filename)
        sheets_result = []
        total_rows = 0
        total_bytes = 0

        for name, df in wb.items():
            est = _estimate_sheet(df, str(name))
            sheets_result.append(est)
            total_rows += est.get("rows", 0)
            total_bytes += est.get("estimated_bytes", 0)

        result = {
            "file_name": filename,
            "file_ext": os.path.splitext(filename or "")[1].lower(),
            "num_sheets": len(wb),
            "total_rows": int(total_rows),
            "estimated_bytes": int(total_bytes),
            "sheets": sheets_result,
        }
        # 若读取失败（wb 为空），以原始文件字节数兜底估算
        if not wb and isinstance(content, (bytes, bytearray)):
            result["estimated_bytes"] = int(len(content))
        return result
    except Exception:
        logger.error(f"估算 Excel 文件失败: {traceback.format_exc()}")
        return {
            "file_name": filename,
            "file_ext": os.path.splitext(filename or "")[1].lower(),
            "num_sheets": 0,
            "total_rows": 0,
            "estimated_bytes": 0,
            "sheets": [],
        }



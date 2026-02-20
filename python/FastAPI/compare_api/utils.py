from typing import Optional, List, Tuple
import pandas as pd
import logging
import traceback
import math
from datetime import datetime, date
import io
import os

logger = logging.getLogger(__name__)


def read_excel_bytes(content: bytes, filename: str = "") -> pd.DataFrame:
    """
    兼容读取 .xlsx / .xls：
    - .xlsx 等：优先 openpyxl
    - .xls：优先尝试 pandas+xlrd；不行则使用 xlrd 直读（兼容 pandas>=3 移除 xlrd 引擎的情况）
    """
    ext = os.path.splitext((filename or "").lower())[1]
    bio = io.BytesIO(content)

    # 兼容“扩展名与实际内容不一致”的情况：
    # - .xls 但内容其实是 xlsx（zip: PK...），例如上游已将 .xls 转成 .xlsx 但仍保留原文件名
    # - .xlsx 但内容是老式 OLE2（D0 CF 11 E0 A1 B1 1A E1）
    is_zip = len(content) >= 2 and content[:2] == b"PK"
    is_ole2 = len(content) >= 8 and content[:8] == b"\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"

    # xlsx family
    if ext in (".xlsx", ".xlsm", ".xltx", ".xltm") or (ext == ".xls" and is_zip):
        return pd.read_excel(bio, engine="openpyxl")

    # legacy xls
    if ext == ".xls" or (ext in (".xlsx", ".xlsm", ".xltx", ".xltm") and is_ole2):
        try:
            bio.seek(0)
            return pd.read_excel(bio, engine="xlrd")
        except Exception:
            return _read_xls_via_xlrd(content)

    # unknown ext: best effort
    try:
        bio.seek(0)
        return pd.read_excel(bio)
    except Exception:
        # 最后兜底：当成 xls 尝试
        try:
            return _read_xls_via_xlrd(content)
        except Exception as e:
            raise e


def _read_xls_via_xlrd(content: bytes) -> pd.DataFrame:
    import xlrd  # xlrd==1.2.0

    book = xlrd.open_workbook(file_contents=content)
    if book.nsheets <= 0:
        return pd.DataFrame()
    sh = book.sheet_by_index(0)
    if sh.nrows <= 0:
        return pd.DataFrame()

    def cvt_cell(cell):
        try:
            if cell.ctype == xlrd.XL_CELL_DATE:
                dt = xlrd.xldate.xldate_as_datetime(cell.value, book.datemode)
                return dt
            if cell.ctype == xlrd.XL_CELL_BOOLEAN:
                return bool(cell.value)
            if cell.ctype == xlrd.XL_CELL_EMPTY or cell.ctype == xlrd.XL_CELL_BLANK:
                return None
        except Exception:
            pass
        return cell.value

    headers = [str(cvt_cell(sh.cell(0, c))).strip() for c in range(sh.ncols)]
    rows = []
    for r in range(1, sh.nrows):
        rows.append([cvt_cell(sh.cell(r, c)) for c in range(sh.ncols)])
    return pd.DataFrame(rows, columns=headers)


def _normalize_scalar_for_compare(v) -> str:
    """
    将单元格值归一化为字符串，用于稳定比对：
    - NaN/None/NaT -> ""
    - 1.0 -> "1"（避免 Excel 数字格式导致主键/数值误判）
    - 字符串去首尾空白
    """
    try:
        if pd.isna(v):
            return ""
    except Exception:
        # 某些对象的 isna 可能抛异常，忽略
        pass

    # 日期时间
    if isinstance(v, (pd.Timestamp, datetime, date)):
        try:
            ts = pd.to_datetime(v)
            # 仅日期
            if ts.hour == 0 and ts.minute == 0 and ts.second == 0 and ts.microsecond == 0:
                return ts.strftime("%Y-%m-%d")
            return ts.strftime("%Y-%m-%d %H:%M:%S")
        except Exception:
            pass

    # numpy 数值/整数兼容（pandas 依赖 numpy，这里按需导入）
    try:
        import numpy as np  # type: ignore

        if isinstance(v, (np.integer,)):
            return str(int(v))
        if isinstance(v, (np.floating,)):
            fv = float(v)
            if math.isfinite(fv) and fv.is_integer():
                return str(int(fv))
            return format(fv, "g")
    except Exception:
        pass

    # Python 原生数值
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, int):
        return str(v)
    if isinstance(v, float):
        if math.isfinite(v) and float(v).is_integer():
            return str(int(v))
        return format(v, "g")

    s = str(v).strip()
    if s.lower() in ("nan", "nat", "none"):
        return ""
    return s


def guess_primary_key_column(df: pd.DataFrame, check_rows: int = 5) -> Optional[str]:
    candidates = []
    possible_names = ['id', '编号', '编码', '资产编号', '序号', '资产号', 'code', 'no', '序列号']

    for col in df.columns:
        values = df[col].head(check_rows)

        if values.isnull().any():
            continue

        if len(set(values)) != len(values):
            continue

        score = 0
        for keyword in possible_names:
            if keyword.lower() in str(col).lower():
                score += 10

        def _pkish(x) -> bool:
            if isinstance(x, bool):
                return True
            if isinstance(x, int):
                return True
            if isinstance(x, float):
                # xls 常见把整数读成 float
                return math.isfinite(x) and float(x).is_integer()
            s = str(x).strip()
            return s.isalnum()

        if values.map(_pkish).all():
            score += 5

        candidates.append((col, score))

    if not candidates:
        return None

    candidates.sort(key=lambda x: -x[1])
    return candidates[0][0]


def prepare_table_data(df: pd.DataFrame, filename: str):
    try:
        if df is None or df.empty:
            return None

        headers = df.columns.tolist()
        data = [headers]

        for _, row in df.iterrows():
            row_data = []
            for col in headers:
                try:
                    value = row[col]
                    if col == '不同列' and isinstance(value, list):
                        row_data.append(','.join(map(str, value)))
                    else:
                        if pd.isna(value):
                            row_data.append(None)
                        else:
                            row_data.append(str(value))
                except Exception:
                    logger.error(f"处理列 {col} 时出错: {traceback.format_exc()}")
                    row_data.append(None)
            data.append(row_data)

        return data
    except Exception:
        logger.error(f"准备表格数据时出错: {traceback.format_exc()}")
        return None


def compare_excel_tables(
    df1: pd.DataFrame,
    df2: pd.DataFrame,
    key: str,
    file1name: str,
    file2name: str,
) -> Tuple[Optional[List[List[str]]], Optional[List[List[str]]], Optional[List[List[str]]]]:
    """
    使用向量化方式比较两个 DataFrame：
    - 按 key 建索引并对齐
    - 计算 only1/only2（减少/增加）
    - 逐列矢量比较，生成不同列列表并分别输出两侧版本
    返回三个表格二维数组（可直接给前端）：reduced, increased, different
    """
    try:
        reduced_df, increased_df, different_df = compare_excel_tables_df(
            df1=df1,
            df2=df2,
            key=key,
            file1name=file1name,
            file2name=file2name,
        )

        # 转换为表格数据（供旧 JSON 接口/前端渲染使用）
        reduced_tbl = prepare_table_data(reduced_df, file1name)
        increased_tbl = prepare_table_data(increased_df, file2name)
        different_tbl = prepare_table_data(different_df, '不同数据') if different_df is not None else None

        return reduced_tbl, increased_tbl, different_tbl
    except Exception:
        logger.error(f"比较数据时出错: {traceback.format_exc()}")
        return None, None, None


def compare_excel_tables_df(
    df1: pd.DataFrame,
    df2: pd.DataFrame,
    key: str,
    file1name: str,
    file2name: str,
) -> Tuple[pd.DataFrame, pd.DataFrame, Optional[pd.DataFrame]]:
    """
    返回 DataFrame 版本结果：
    - reduced_df：只在文件1（减少项）
    - increased_df：只在文件2（增加项）
    - different_df：同一主键内容不一致（两侧各一行，含 文件来源/不同列）
    """
    reduced_df, increased_df, ordered_cols, left_rows, right_rows, diff_mask = compare_excel_tables_artifacts(df1, df2, key)
    if diff_mask is None or left_rows is None or right_rows is None or left_rows.empty:
        return reduced_df, increased_df, None

    # 生成“不同列”列表：用 numpy 一次性聚合，避免逐行 apply(axis=1) 的 Python 循环开销。
    import numpy as np  # pandas 依赖 numpy，这里按需导入

    arr = diff_mask.to_numpy()
    col_names = list(diff_mask.columns)
    row_keys = list(diff_mask.index)
    rpos, cpos = np.where(arr)
    buckets = {k: [] for k in row_keys}
    for i, j in zip(rpos.tolist(), cpos.tolist()):
        buckets[row_keys[i]].append(col_names[j])
    different_cols_series = pd.Series(buckets)

    left_diff = left_rows.reset_index()
    left_diff["文件来源"] = file1name
    left_diff["不同列"] = left_diff[key].map(different_cols_series)

    right_diff = right_rows.reset_index()
    right_diff["文件来源"] = file2name
    right_diff["不同列"] = right_diff[key].map(different_cols_series)

    different_df = pd.concat([left_diff, right_diff], ignore_index=True)
    return reduced_df, increased_df, different_df


def compare_excel_tables_artifacts(
    df1: pd.DataFrame,
    df2: pd.DataFrame,
    key: str,
) -> Tuple[pd.DataFrame, pd.DataFrame, List[str], Optional[pd.DataFrame], Optional[pd.DataFrame], Optional[pd.DataFrame]]:
    """
    计算一次并返回导出/渲染需要的“对照信息”：
    - reduced_df / increased_df：保持各自原文件列顺序
    - ordered_cols：用于并列对照的字段顺序（按 df1，再补 df2 新列）
    - left_rows/right_rows：仅包含有差异的主键行（index=key），列为 ordered_cols
    - diff_mask：与 left_rows/right_rows 对齐的 bool mask（True 表示该字段两侧不同）
    """
    # 保留原始列顺序（导出/展示时应与原文件一致）
    orig_cols1 = list(df1.columns)
    orig_cols2 = list(df2.columns)

    # 统一 key（关键修复：避免 1 vs 1.0 导致主键对不齐）
    df1_c = df1.copy()
    df2_c = df2.copy()
    df1_c[key] = df1_c[key].map(_normalize_scalar_for_compare)
    df2_c[key] = df2_c[key].map(_normalize_scalar_for_compare)
    df1_c = df1_c[df1_c[key].astype(str).str.strip() != ""]
    df2_c = df2_c[df2_c[key].astype(str).str.strip() != ""]

    # 主键必须唯一：否则 drop_duplicates 会静默吞行，导致结果不准确
    dup1 = df1_c[key].duplicated(keep=False)
    if dup1.any():
        keys = df1_c.loc[dup1, key].astype(str).head(10).tolist()
        raise ValueError(f"文件1主键列“{key}”存在重复值（示例: {keys}），请先去重或修正后再比对")
    dup2 = df2_c[key].duplicated(keep=False)
    if dup2.any():
        keys = df2_c.loc[dup2, key].astype(str).head(10).tolist()
        raise ValueError(f"文件2主键列“{key}”存在重复值（示例: {keys}），请先去重或修正后再比对")

    # 设为索引
    df1_c = df1_c.set_index(key)
    df2_c = df2_c.set_index(key)

    only1_idx = df1_c.index.difference(df2_c.index)
    only2_idx = df2_c.index.difference(df1_c.index)
    common_idx = df1_c.index.intersection(df2_c.index)

    reduced_df = df1_c.loc[only1_idx].reset_index()
    increased_df = df2_c.loc[only2_idx].reset_index()
    if not reduced_df.empty:
        reduced_df = reduced_df.reindex(columns=[c for c in orig_cols1 if c in reduced_df.columns])
    if not increased_df.empty:
        increased_df = increased_df.reindex(columns=[c for c in orig_cols2 if c in increased_df.columns])

    # ordered union columns (exclude key because it's index)
    ordered_cols = [c for c in orig_cols1 if c != key and c in set(df1_c.columns) | set(df2_c.columns)]
    for c in orig_cols2:
        if c == key:
            continue
        if c not in ordered_cols and c in set(df1_c.columns) | set(df2_c.columns):
            ordered_cols.append(c)

    if len(common_idx) == 0:
        return reduced_df, increased_df, ordered_cols, None, None, None

    a = df1_c.loc[common_idx].reindex(columns=ordered_cols)
    b = df2_c.loc[common_idx].reindex(columns=ordered_cols)

    diff_fast = None
    cand_cols = None
    try:
        diff_fast = a.ne(b)
        both_na = a.isna() & b.isna()
        diff_fast = diff_fast & (~both_na)
        cand_rows = diff_fast.any(axis=1)
        if cand_rows.any():
            cand_cols = diff_fast.loc[cand_rows].any(axis=0)
    except Exception:
        cand_rows = pd.Series(True, index=a.index)

    if not cand_rows.any():
        return reduced_df, increased_df, ordered_cols, None, None, None

    a_sub = a.loc[cand_rows]
    b_sub = b.loc[cand_rows]

    if cand_cols is not None and getattr(cand_cols, "any", lambda: False)():
        cols_to_check = [c for c, v in cand_cols.items() if bool(v)]
        if not cols_to_check:
            cols_to_check = ordered_cols
    else:
        cols_to_check = ordered_cols

    a_cmp = a_sub[cols_to_check].apply(lambda s: s.map(_normalize_scalar_for_compare))
    b_cmp = b_sub[cols_to_check].apply(lambda s: s.map(_normalize_scalar_for_compare))
    diff_mask_check = a_cmp.ne(b_cmp)
    rows_with_diff = diff_mask_check.any(axis=1)
    if not rows_with_diff.any():
        return reduced_df, increased_df, ordered_cols, None, None, None

    # Expand to full ordered_cols for downstream highlighting (cols not checked are treated as equal).
    diff_mask = pd.DataFrame(False, index=diff_mask_check.index, columns=ordered_cols)
    diff_mask.loc[:, cols_to_check] = diff_mask_check

    left_rows = a_sub.loc[rows_with_diff]
    right_rows = b_sub.loc[rows_with_diff]
    diff_mask = diff_mask.loc[rows_with_diff]
    return reduced_df, increased_df, ordered_cols, left_rows, right_rows, diff_mask


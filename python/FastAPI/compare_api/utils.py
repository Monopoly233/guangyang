from typing import Optional, List, Tuple
import pandas as pd
import logging
import traceback

logger = logging.getLogger(__name__)


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

        if values.map(lambda x: isinstance(x, int) or str(x).strip().isalnum()).all():
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
        # 统一 key 为字符串，去重并设为索引
        df1_c = df1.copy()
        df2_c = df2.copy()
        df1_c[key] = df1_c[key].astype(str)
        df2_c[key] = df2_c[key].astype(str)
        df1_c = df1_c.drop_duplicates(subset=[key], keep='first').set_index(key)
        df2_c = df2_c.drop_duplicates(subset=[key], keep='first').set_index(key)

        # 计算集合
        only1_idx = df1_c.index.difference(df2_c.index)
        only2_idx = df2_c.index.difference(df1_c.index)
        common_idx = df1_c.index.intersection(df2_c.index)

        # 统一列集合并对齐
        a = df1_c.loc[common_idx]
        b = df2_c.loc[common_idx]
        all_cols = sorted(set(a.columns) | set(b.columns))
        a = a.reindex(columns=all_cols)
        b = b.reindex(columns=all_cols)

        # 字符串化后矢量比较
        a_cmp = a.astype(str).fillna('')
        b_cmp = b.astype(str).fillna('')
        diff_mask = a_cmp.ne(b_cmp)
        rows_with_diff = diff_mask.any(axis=1)

        # 为不同的行收集不同列
        different_cols_series = None
        if rows_with_diff.any():
            different_cols_series = diff_mask[rows_with_diff].apply(lambda r: list(r.index[r.values]), axis=1)

        # 生成 reduced / increased 结果
        reduced_df = df1_c.loc[only1_idx].reset_index()
        increased_df = df2_c.loc[only2_idx].reset_index()

        # 生成 different 结果（两边各一行）
        different_df = None
        if rows_with_diff.any():
            left_diff = a.loc[rows_with_diff].reset_index()
            left_diff['文件来源'] = file1name
            left_diff['不同列'] = left_diff[key].map(different_cols_series) if key in left_diff.columns else different_cols_series.values

            right_diff = b.loc[rows_with_diff].reset_index()
            right_diff['文件来源'] = file2name
            right_diff['不同列'] = right_diff[key].map(different_cols_series) if key in right_diff.columns else different_cols_series.values

            different_df = pd.concat([left_diff, right_diff], ignore_index=True)

        # 转换为表格数据
        reduced_tbl = prepare_table_data(reduced_df, file1name)
        increased_tbl = prepare_table_data(increased_df, file2name)
        different_tbl = prepare_table_data(different_df, '不同数据') if different_df is not None else None

        return reduced_tbl, increased_tbl, different_tbl
    except Exception:
        logger.error(f"比较数据时出错: {traceback.format_exc()}")
        return None, None, None


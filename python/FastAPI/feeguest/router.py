from fastapi import APIRouter, UploadFile, File, HTTPException, Query
from typing import List, Dict, Any
import logging
import traceback

from .utils import estimate_excel_file


logger = logging.getLogger(__name__)


router = APIRouter(prefix="", tags=["feeguest"])


@router.post("/feeguest/estimate")
async def estimate_fee(
    files: List[UploadFile] = File(..., description="一个或多个 Excel 文件 (.xls/.xlsx)"),
    pricing_mode: str = Query("mb", regex="^(mb|rows)$", description="计费模式：mb 或 rows"),
    rate_per_mb: float = Query(0.1, ge=0, description="按 MB 计费费率，单位/MB"),
    rate_per_1k_rows: float = Query(0.5, ge=0, description="按每千行计费费率，单位/千行"),
) -> Dict[str, Any]:
    try:
        if not files:
            raise HTTPException(status_code=400, detail="请至少上传一个 Excel 文件")

        results = []
        total_rows_across_files = 0
        total_bytes_across_files = 0

        for f in files:
            content = await f.read()
            if not content:
                raise HTTPException(status_code=400, detail=f"文件 {f.filename} 内容为空")

            est = estimate_excel_file(content, f.filename)
            results.append(est)
            total_rows_across_files += est.get("total_rows", 0)
            total_bytes_across_files += est.get("estimated_bytes", 0)

        mb = total_bytes_across_files / (1024 * 1024)
        fee_by_mb = mb * rate_per_mb
        fee_by_rows = (total_rows_across_files / 1000.0) * rate_per_1k_rows

        # 最低收费 1 元；超过时按原计算值收取
        base_min_fee = 1.0
        raw_fee = fee_by_mb if pricing_mode == "mb" else fee_by_rows
        chosen_fee = max(base_min_fee, raw_fee)

        return {
            "total_files": len(files),
            "total_rows": int(total_rows_across_files),
            "estimated_bytes": int(total_bytes_across_files),
            "estimated_size_mb": mb,
            "pricing": {
                "mode": pricing_mode,
                "rate_per_mb": rate_per_mb,
                "rate_per_1k_rows": rate_per_1k_rows,
                "fee_by_mb": fee_by_mb,
                "fee_by_rows": fee_by_rows,
                "chosen_fee": round(chosen_fee, 2),
                "base_min_fee": base_min_fee,
            },
            "files": results,
        }
    except HTTPException:
        raise
    except Exception:
        logger.error(f"估算费用接口出错: {traceback.format_exc()}")
        raise HTTPException(status_code=500, detail="服务器内部错误")



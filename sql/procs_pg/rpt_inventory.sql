/* =============================================================
   rpt — inventory / stock reports (PostgreSQL port over public.inventory)
   product_key = release_id OR 9e9 + hardware_id.
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_StockByShop(p_shop_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT i.condition,
           COUNT(*) AS sku_rows,
           SUM(i.on_hand) AS on_hand, SUM(i.on_order) AS on_order, SUM(i.reserved) AS reserved
    FROM public.inventory i
    WHERE i.shop_id = p_shop_key
    GROUP BY i.condition ORDER BY on_hand DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_StockByPlatform(p_shop_key bigint DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(p.platform_name, '(unknown)') AS platform_name,
           SUM(i.on_hand) AS on_hand, COUNT(*) AS sku_rows
    FROM public.inventory i
    JOIN dw.dim_product p ON p.product_key = CASE WHEN i.release_id IS NOT NULL THEN i.release_id
                                                  ELSE 9000000000 + i.hardware_id END
    WHERE (p_shop_key IS NULL OR i.shop_id = p_shop_key)
    GROUP BY p.platform_name ORDER BY on_hand DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_DeadStock(p_as_of date DEFAULT '2016-09-30', p_stale_days int DEFAULT 365, p_top int DEFAULT 100)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT i.shop_id, p.title, p.platform_name, i.condition, i.on_hand, i.last_movement_at
    FROM public.inventory i
    JOIN dw.dim_product p ON p.product_key = CASE WHEN i.release_id IS NOT NULL THEN i.release_id
                                                  ELSE 9000000000 + i.hardware_id END
    WHERE i.on_hand > 0
      AND (i.last_movement_at IS NULL OR i.last_movement_at < (p_as_of - p_stale_days))
    ORDER BY i.on_hand DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_LowStockAlerts(p_shop_key bigint, p_threshold int DEFAULT 2)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.title, p.platform_name, i.condition, i.on_hand, i.on_order
    FROM public.inventory i
    JOIN dw.dim_product p ON p.product_key = CASE WHEN i.release_id IS NOT NULL THEN i.release_id
                                                  ELSE 9000000000 + i.hardware_id END
    WHERE i.shop_id = p_shop_key AND i.on_hand <= p_threshold AND i.condition = 'new'
    ORDER BY i.on_hand;
    RETURN c;
END $$;

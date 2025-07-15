<?php
// mexc_sniper.php — superszybki bot MEXC w PHP (market buy + take profit + latency table)

$listing = json_decode(file_get_contents(__DIR__ . '/current_listing.json'), true);
if (!$listing) die("❌ Nie mogę odczytać current_listing.json\n");

$api_key     = $listing['api_key'];
$api_secret  = $listing['api_secret'];
$symbol      = strtoupper($listing['symbol']);
$quote_amount= floatval($listing['quote_amount']);
$profit_pct  = floatval($listing['profit_pct'] ?? 15);

function now_ms() { return round(microtime(true) * 1000); }

function market_buy($symbol, $quote_amount, $api_key, $api_secret, &$latency_ms = null, &$send_ms = null, &$recv_ms = null) {
    $url = 'https://api.mexc.com/api/v3/order';
    $params = [
        'symbol'        => $symbol,
        'side'          => 'BUY',
        'type'          => 'MARKET',
        'quoteOrderQty' => $quote_amount,
        'timestamp'     => now_ms(),
        'recvWindow'    => 5000
    ];
    ksort($params);
    $query = http_build_query($params, '', '&', PHP_QUERY_RFC3986);
    $signature = hash_hmac('sha256', $query, $api_secret);
    $query .= '&signature=' . $signature;

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, $url . '?' . $query);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'X-MEXC-APIKEY: ' . $api_key,
        'Content-Type: application/json'
    ]);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, '');

    $send = now_ms();
    $resp = curl_exec($ch);
    $recv = now_ms();
    $latency = $recv - $send;

    $httpcode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    curl_close($ch);
    if ($httpcode != 200) die("❌ MARKET BUY fail $resp\n");
    $data = json_decode($resp, true);
    echo "🎉 MARKET BUY ok: orderId=" . $data['orderId'] . "\n";
    $latency_ms = $latency;
    $send_ms = $send;
    $recv_ms = $recv;
    return $data['orderId'];
}

function fetch_order($symbol, $orderId, $api_key, $api_secret) {
    $url = 'https://api.mexc.com/api/v3/order';
    $params = [
        'symbol'    => $symbol,
        'orderId'   => $orderId,
        'timestamp' => now_ms(),
        'recvWindow'=> 5000
    ];
    ksort($params);
    $query = http_build_query($params, '', '&', PHP_QUERY_RFC3986);
    $signature = hash_hmac('sha256', $query, $api_secret);
    $query .= '&signature=' . $signature;

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, $url . '?' . $query);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'X-MEXC-APIKEY: ' . $api_key
    ]);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    $resp = curl_exec($ch);
    curl_close($ch);
    $data = json_decode($resp, true);
    return [
        'executedQty' => floatval($data['executedQty']),
        'price'       => floatval($data['price'])
    ];
}

function limit_sell($symbol, $qty, $price, $api_key, $api_secret, &$latency_ms = null, &$send_ms = null, &$recv_ms = null) {
    $url = 'https://api.mexc.com/api/v3/order';
    $params = [
        'symbol'    => $symbol,
        'side'      => 'SELL',
        'type'      => 'LIMIT',
        'timeInForce'=> 'GTC',
        'quantity'  => sprintf('%.8f', $qty),
        'price'     => sprintf('%.8f', $price),
        'timestamp' => now_ms(),
        'recvWindow'=> 5000
    ];
    ksort($params);
    $query = http_build_query($params, '', '&', PHP_QUERY_RFC3986);
    $signature = hash_hmac('sha256', $query, $api_secret);
    $query .= '&signature=' . $signature;

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, $url . '?' . $query);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'X-MEXC-APIKEY: ' . $api_key,
        'Content-Type: application/json'
    ]);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, '');

    $send = now_ms();
    $resp = curl_exec($ch);
    $recv = now_ms();
    $latency = $recv - $send;

    $httpcode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    curl_close($ch);
    if ($httpcode != 200) die("❌ LIMIT SELL fail $resp\n");
    $data = json_decode($resp, true);
    echo "🎯 TP SELL: orderId=" . $data['orderId'] . "\n";
    $latency_ms = $latency;
    $send_ms = $send;
    $recv_ms = $recv;
}

// ---- Główna logika ----
echo "📊 Performance:\n";
printf("%-23s | %-23s | %-9s | %-6s\n", "Sent", "Received", "Latency", "Type");

$mb_latency = $mb_send = $mb_recv = null;
$orderId = market_buy($symbol, $quote_amount, $api_key, $api_secret, $mb_latency, $mb_send, $mb_recv);
printf("%-23s | %-23s | %9d | MARKET BUY\n",
    date('H:i:s.v', intval($mb_send/1000)).sprintf("%03d",$mb_send%1000),
    date('H:i:s.v', intval($mb_recv/1000)).sprintf("%03d",$mb_recv%1000),
    $mb_latency
);

sleep(1);
$details = fetch_order($symbol, $orderId, $api_key, $api_secret);

$tp_price = $details['price'] * (1.0 + $profit_pct/100.0);
$ls_latency = $ls_send = $ls_recv = null;
limit_sell($symbol, $details['executedQty'], $tp_price, $api_key, $api_secret, $ls_latency, $ls_send, $ls_recv);

printf("%-23s | %-23s | %9d | LIMIT SELL\n",
    date('H:i:s.v', intval($ls_send/1000)).sprintf("%03d",$ls_send%1000),
    date('H:i:s.v', intval($ls_recv/1000)).sprintf("%03d",$ls_recv%1000),
    $ls_latency
);

echo "✅ Zakończono\n";

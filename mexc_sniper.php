
<?php
// Plik: mexc_sniper.php
// WYMAGA: PHP z curl (apt install php php-curl)

// 1. Wczytaj parametry z JSON (current_listing.json)
$current = json_decode(file_get_contents(__DIR__ . "/current_listing.json"), true);
$symbol        = strtoupper($current['symbol']);
$quote_amount  = floatval($current['quote_amount']);
$api_key       = $current['api_key'];
$api_secret    = $current['api_secret'];
$profit_pct    = isset($current['profit_pct']) ? floatval($current['profit_pct']) : 12.0;

// 2. MARKET BUY
$timestamp = round(microtime(true) * 1000);
$query = "symbol=$symbol&side=BUY&type=MARKET&quoteOrderQty=$quote_amount&timestamp=$timestamp&recvWindow=5000";
$signature = hash_hmac('sha256', $query, $api_secret);
$url = "https://api.mexc.com/api/v3/order?$query&signature=$signature";

$ch = curl_init($url);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    "X-MEXC-APIKEY: $api_key",
    "Content-Type: application/json"
]);
curl_setopt($ch, CURLOPT_POST, 1);
curl_setopt($ch, CURLOPT_RETURNTRANSFER, 1);
curl_setopt($ch, CURLOPT_POSTFIELDS, ""); // empty body for POST
$response = curl_exec($ch);
curl_close($ch);

$order = json_decode($response, true);
if (!isset($order['orderId'])) {
    echo "❌ Błąd MARKET BUY:\n$response\n";
    exit(1);
}
echo "✅ MARKET BUY: $symbol za $quote_amount\n";

// 3. Pobierz szczegóły zlecenia (żeby znać dokładną ilość kupionych tokenów)
sleep(1); // chwila na wykonanie market buy
$order_id = $order['orderId'];
$timestamp = round(microtime(true) * 1000);
$query = "symbol=$symbol&orderId=$order_id&timestamp=$timestamp&recvWindow=5000";
$signature = hash_hmac('sha256', $query, $api_secret);
$url = "https://api.mexc.com/api/v3/order?$query&signature=$signature";
$ch = curl_init($url);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    "X-MEXC-APIKEY: $api_key",
    "Content-Type: application/json"
]);
curl_setopt($ch, CURLOPT_RETURNTRANSFER, 1);
$response = curl_exec($ch);
curl_close($ch);

$details = json_decode($response, true);
$qty = isset($details['executedQty']) ? floatval($details['executedQty']) : 0.0;
$price = isset($details['price']) ? floatval($details['price']) : 0.0;

if ($qty == 0.0 || $price == 0.0) {
    echo "❌ Błąd pobrania szczegółów zamówienia:\n$response\n";
    exit(1);
}
echo "✅ KUPIONE: $qty $symbol po cenie $price\n";

// 4. LIMIT SELL (Take-Profit)
$tp_price = round($price * (1 + $profit_pct / 100), 8);
$timestamp = round(microtime(true) * 1000);
$query = "symbol=$symbol&side=SELL&type=LIMIT&timeInForce=GTC&quantity=" . number_format($qty, 8, '.', '') . "&price=" . number_format($tp_price, 8, '.', '') . "&timestamp=$timestamp&recvWindow=5000";
$signature = hash_hmac('sha256', $query, $api_secret);
$url = "https://api.mexc.com/api/v3/order?$query&signature=$signature";

$ch = curl_init($url);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    "X-MEXC-APIKEY: $api_key",
    "Content-Type: application/json"
]);
curl_setopt($ch, CURLOPT_POST, 1);
curl_setopt($ch, CURLOPT_RETURNTRANSFER, 1);
curl_setopt($ch, CURLOPT_POSTFIELDS, "");
$response = curl_exec($ch);
curl_close($ch);

$tp = json_decode($response, true);
if (isset($tp['orderId'])) {
    echo "🎯 TP LIMIT SELL: $qty $symbol @ $tp_price wysłane.\n";
} else {
    echo "❌ Błąd LIMIT SELL:\n$response\n";
    exit(1);
}
?>

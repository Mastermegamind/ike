<?php
/**
 * Ike provisioning module for WHMCS.
 *
 * Install: copy this folder to <whmcs>/modules/servers/ike/
 * Server setup in WHMCS (System Settings → Servers → Add New Server):
 *   Module:     Ike
 *   Hostname:   your panel address, e.g. panel.example.com
 *   Port:       2087 for an admin token, 2086 for a reseller token (Override with Custom Port)
 *   Secure:     on
 *   Access Hash: an Ike API token (Settings → API tokens in the Ike panel)
 * Product setup (Module Settings tab): Ike package name, as shown under Packages in Ike.
 *
 * Licensed AGPL-3.0, like the rest of Ike.
 */

if (!defined('WHMCS')) {
    die('This file cannot be accessed directly');
}

use WHMCS\Database\Capsule;

function ike_MetaData()
{
    return [
        'DisplayName' => 'Ike',
        'APIVersion' => '1.1',
        'RequiresServer' => true,
        'DefaultNonSSLPort' => '2087',
        'DefaultSSLPort' => '2087',
        'ServiceSingleSignOnLabel' => 'Log in to Ike',
        'AdminSingleSignOnLabel' => 'Log in to Ike as the customer',
    ];
}

function ike_ConfigOptions()
{
    return [
        'Package' => [
            'Type' => 'text',
            'Size' => '30',
            'Description' => 'Ike hosting package name or ID',
            'SimpleMode' => true,
        ],
        'Runtime' => [
            'Type' => 'dropdown',
            'Options' => 'php,static,node,python',
            'Default' => 'php',
            'Description' => 'What the first website runs',
            'SimpleMode' => true,
        ],
    ];
}

/** Calls the Ike provisioning API and returns the decoded answer. */
function ike_api(array $params, $method, $path, $body = null)
{
    $host = $params['serverhostname'] ?: $params['serverip'];
    $port = $params['serverport'] ?: '2087';
    $url = 'https://' . $host . ':' . $port . '/api/v1' . $path;
    $token = trim($params['serveraccesshash']);
    if ($token === '') {
        $token = trim($params['serverpassword']);
    }
    $ch = curl_init($url);
    $headers = ['Authorization: Bearer ' . $token, 'Accept: application/json'];
    curl_setopt($ch, CURLOPT_CUSTOMREQUEST, $method);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_TIMEOUT, 300);
    // The panel ships with a self-signed certificate. Put a real certificate
    // on the panel (Settings → Panel address) and set this to true.
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, (bool) getenv('IKE_VERIFY_TLS'));
    curl_setopt($ch, CURLOPT_SSL_VERIFYHOST, getenv('IKE_VERIFY_TLS') ? 2 : 0);
    if ($body !== null) {
        $json = json_encode($body);
        $headers[] = 'Content-Type: application/json';
        curl_setopt($ch, CURLOPT_POSTFIELDS, $json);
    }
    curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);
    $raw = curl_exec($ch);
    $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $err = curl_error($ch);
    curl_close($ch);

    $logBody = $body;
    if (is_array($logBody) && isset($logBody['password'])) {
        $logBody['password'] = '********';
    }
    logModuleCall('ike', $method . ' ' . $path, $logBody, $raw, null, [$token, $params['password'] ?? '']);

    if ($raw === false) {
        throw new Exception('Could not reach the Ike panel: ' . $err);
    }
    $data = json_decode($raw, true);
    if ($code >= 300) {
        $msg = is_array($data) && isset($data['error']) ? $data['error'] : ('HTTP ' . $code);
        throw new Exception($msg);
    }
    return $data;
}

/** Ike account names: lowercase letters and digits, starting with a letter. */
function ike_account(array $params)
{
    $u = strtolower(preg_replace('/[^a-z0-9]/i', '', $params['username']));
    if ($u === '' || !ctype_alpha($u[0])) {
        $u = 'u' . $u;
    }
    return substr($u, 0, 16);
}

function ike_CreateAccount(array $params)
{
    try {
        $account = ike_account($params);
        $res = ike_api($params, 'POST', '/provision/accounts', [
            'account' => $account,
            'domain' => $params['domain'],
            'package' => $params['configoption1'],
            'runtime' => $params['configoption2'] ?: 'php',
            'email' => $params['clientsdetails']['email'],
            'username' => $account,
            'password' => $params['password'],
        ]);
        if ($account !== $params['username']) {
            Capsule::table('tblhosting')->where('id', $params['serviceid'])->update(['username' => $account]);
        }
        return 'success';
    } catch (Exception $e) {
        return $e->getMessage();
    }
}

function ike_SuspendAccount(array $params)
{
    try {
        ike_api($params, 'POST', '/provision/accounts/' . ike_account($params) . '/suspend',
            ['reason' => $params['suspendreason'] ?: 'Suspended by billing']);
        return 'success';
    } catch (Exception $e) {
        return $e->getMessage();
    }
}

function ike_UnsuspendAccount(array $params)
{
    try {
        ike_api($params, 'POST', '/provision/accounts/' . ike_account($params) . '/unsuspend', new stdClass());
        return 'success';
    } catch (Exception $e) {
        return $e->getMessage();
    }
}

function ike_TerminateAccount(array $params)
{
    try {
        ike_api($params, 'DELETE', '/provision/accounts/' . ike_account($params));
        return 'success';
    } catch (Exception $e) {
        return $e->getMessage();
    }
}

function ike_ChangePackage(array $params)
{
    try {
        ike_api($params, 'PUT', '/provision/accounts/' . ike_account($params) . '/package', ['package' => $params['configoption1']]);
        return 'success';
    } catch (Exception $e) {
        return $e->getMessage();
    }
}

function ike_ChangePassword(array $params)
{
    try {
        ike_api($params, 'PUT', '/provision/accounts/' . ike_account($params) . '/password', ['password' => $params['password']]);
        return 'success';
    } catch (Exception $e) {
        return $e->getMessage();
    }
}

function ike_TestConnection(array $params)
{
    try {
        ike_api($params, 'GET', '/provision/packages');
        return ['success' => true, 'error' => ''];
    } catch (Exception $e) {
        return ['success' => false, 'error' => $e->getMessage()];
    }
}

function ike_ServiceSingleSignOn(array $params)
{
    try {
        $res = ike_api($params, 'POST', '/provision/accounts/' . ike_account($params) . '/sso', new stdClass());
        return ['success' => true, 'redirectTo' => $res['url']];
    } catch (Exception $e) {
        return ['success' => false, 'errorMsg' => $e->getMessage()];
    }
}

function ike_AdminSingleSignOn(array $params)
{
    return ike_ServiceSingleSignOn($params);
}

/** Runs from the WHMCS daily cron: disk and bandwidth for every service on this server. */
function ike_UsageUpdate(array $params)
{
    try {
        $rows = ike_api($params, 'GET', '/provision/usage');
    } catch (Exception $e) {
        return $e->getMessage();
    }
    foreach ($rows as $row) {
        Capsule::table('tblhosting')
            ->where('server', $params['serverid'])
            ->where('username', $row['account'])
            ->update([
                'diskusage' => (int) round($row['disk_mb']),
                'disklimit' => (int) $row['disk_limit_mb'],
                'bwusage' => (int) round($row['bandwidth_mb']),
                'bwlimit' => (int) $row['bandwidth_limit_mb'],
                'lastupdate' => Capsule::raw('now()'),
            ]);
    }
    return 'success';
}

function ike_ClientArea(array $params)
{
    return [
        'templatefile' => 'clientarea',
        'vars' => ['panelUrl' => 'https://' . ($params['serverhostname'] ?: $params['serverip']) . ':2083'],
    ];
}

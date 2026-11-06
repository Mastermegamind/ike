<?php
/**
 * Ike provisioning module for Blesta.
 *
 * Install: copy this folder to <blesta>/components/modules/ike/, then
 * Settings → Company → Modules → Available → Ike → Install, and add a
 * server with the panel address and an Ike API token.
 *
 * Licensed AGPL-3.0, like the rest of Ike.
 */
class Ike extends Module
{
    public function __construct()
    {
        Language::loadLang('ike', null, dirname(__FILE__) . DS . 'language' . DS);
        $this->loadConfig(dirname(__FILE__) . DS . 'config.json');
        Loader::loadComponents($this, ['Input']);
    }

    // ------------------------------------------------------------ servers

    public function manageModule($module, array &$vars)
    {
        $this->view = new View('manage', 'default');
        $this->view->base_uri = $this->base_uri;
        $this->view->setDefaultView('components' . DS . 'modules' . DS . 'ike' . DS);
        Loader::loadHelpers($this, ['Form', 'Html', 'Widget']);
        $this->view->set('module', $module);
        return $this->view->fetch();
    }

    public function manageAddRow(array &$vars)
    {
        return $this->rowView('add_row', $vars);
    }

    public function manageEditRow($module_row, array &$vars)
    {
        if (empty($vars)) {
            $vars = (array) $module_row->meta;
        }
        return $this->rowView('edit_row', $vars);
    }

    private function rowView($name, array $vars)
    {
        $this->view = new View($name, 'default');
        $this->view->base_uri = $this->base_uri;
        $this->view->setDefaultView('components' . DS . 'modules' . DS . 'ike' . DS);
        Loader::loadHelpers($this, ['Form', 'Html', 'Widget']);
        $this->view->set('vars', (object) $vars);
        return $this->view->fetch();
    }

    public function addModuleRow(array &$vars)
    {
        return $this->saveRow($vars);
    }

    public function editModuleRow($module_row, array &$vars)
    {
        if (empty($vars['token'])) {
            $vars['token'] = $module_row->meta->token;
        }
        return $this->saveRow($vars);
    }

    private function saveRow(array &$vars)
    {
        $vars['port'] = trim($vars['port'] ?? '') ?: '2087';
        $this->Input->setRules([
            'hostname' => ['valid' => ['rule' => [[$this, 'validHost']], 'message' => Language::_('Ike.!error.hostname.valid', true)]],
            'port' => ['valid' => ['rule' => 'is_numeric', 'message' => Language::_('Ike.!error.port.valid', true)]],
            'token' => [
                'valid' => ['rule' => ['matches', '/^ike_[A-Za-z0-9_-]{20,}$/'], 'message' => Language::_('Ike.!error.token.valid', true)],
                'connect' => ['rule' => [[$this, 'canConnect'], $vars['hostname'] ?? '', $vars['port']], 'message' => Language::_('Ike.!error.token.connect', true)],
            ],
        ]);
        if (!$this->Input->validates($vars)) {
            return;
        }
        return [
            ['key' => 'hostname', 'value' => trim($vars['hostname']), 'encrypted' => 0],
            ['key' => 'port', 'value' => $vars['port'], 'encrypted' => 0],
            ['key' => 'token', 'value' => trim($vars['token']), 'encrypted' => 1],
        ];
    }

    public function validHost($h)
    {
        return (bool) preg_match('/^[a-z0-9.-]{1,253}$/i', trim((string) $h));
    }

    public function canConnect($token, $host, $port)
    {
        try {
            $this->call((object) ['hostname' => $host, 'port' => $port, 'token' => $token], 'GET', '/provision/packages');
            return true;
        } catch (Exception $e) {
            return false;
        }
    }

    // ------------------------------------------------------------ packages

    public function getPackageFields($vars = null)
    {
        Loader::loadHelpers($this, ['Html']);
        $fields = new ModuleFields();
        $pkg = $fields->label(Language::_('Ike.package_fields.package', true), 'ike_package');
        $pkg->attach($fields->fieldText('meta[package]', $vars->meta['package'] ?? null, ['id' => 'ike_package']));
        $fields->setField($pkg);
        return $fields;
    }

    public function addPackage(array $vars = null)
    {
        return $this->packageMeta($vars);
    }

    public function editPackage($package, array $vars = null)
    {
        return $this->packageMeta($vars);
    }

    private function packageMeta(array $vars = null)
    {
        $this->Input->setRules(['meta[package]' => ['empty' => ['rule' => 'isEmpty', 'negate' => true, 'message' => Language::_('Ike.!error.package.empty', true)]]]);
        if (!$this->Input->validates($vars)) {
            return;
        }
        return [['key' => 'package', 'value' => trim($vars['meta']['package']), 'encrypted' => 0]];
    }

    // ------------------------------------------------------------ services

    public function getClientAddFields($package, $vars = null)
    {
        return $this->domainField($vars);
    }

    public function getAdminAddFields($package, $vars = null)
    {
        return $this->domainField($vars);
    }

    private function domainField($vars)
    {
        Loader::loadHelpers($this, ['Html']);
        $fields = new ModuleFields();
        $d = $fields->label(Language::_('Ike.service_fields.domain', true), 'ike_domain');
        $d->attach($fields->fieldText('ike_domain', $vars->ike_domain ?? ($vars->domain ?? null), ['id' => 'ike_domain']));
        $fields->setField($d);
        return $fields;
    }

    /** Builds an account name from the domain: shop.example.com → shopexample. */
    private function accountName($domain)
    {
        $base = strtolower(preg_replace('/[^a-z0-9]/i', '', preg_replace('/\.[a-z.]+$/i', '', $domain)));
        if ($base === '' || !ctype_alpha($base[0])) {
            $base = 'u' . $base;
        }
        return substr($base, 0, 10) . substr(bin2hex(random_bytes(2)), 0, 3);
    }

    public function addService($package, array $vars = null, $parent_package = null, $parent_service = null, $status = 'pending')
    {
        $domain = strtolower(trim($vars['ike_domain'] ?? ''));
        if (!preg_match('/^([a-z0-9-]+\.)+[a-z]{2,}$/', $domain)) {
            $this->Input->setErrors(['ike_domain' => ['format' => Language::_('Ike.!error.domain.format', true)]]);
            return;
        }
        $account = $this->accountName($domain);
        $password = substr(str_replace(['+', '/', '='], '', base64_encode(random_bytes(18))), 0, 16);
        if (($vars['use_module'] ?? 'true') === 'true') {
            $client = $this->client($vars['client_id'] ?? null);
            try {
                $this->call($this->getModuleRow(), 'POST', '/provision/accounts', [
                    'account' => $account,
                    'domain' => $domain,
                    'package' => $package->meta->package,
                    'email' => $client ? $client->email : '',
                    'username' => $account,
                    'password' => $password,
                ]);
            } catch (Exception $e) {
                $this->Input->setErrors(['api' => ['internal' => $e->getMessage()]]);
                return;
            }
        }
        return [
            ['key' => 'ike_domain', 'value' => $domain, 'encrypted' => 0],
            ['key' => 'ike_account', 'value' => $account, 'encrypted' => 0],
            ['key' => 'ike_password', 'value' => $password, 'encrypted' => 1],
        ];
    }

    private function client($id)
    {
        if (!$id) {
            return null;
        }
        Loader::loadModels($this, ['Clients']);
        return $this->Clients->get($id, false);
    }

    private function account($service)
    {
        return $this->serviceFieldsToObject($service->fields)->ike_account;
    }

    private function simple($service, $method, $suffix, $body = null)
    {
        try {
            $this->call($this->getModuleRow($service->module_row_id), $method, '/provision/accounts/' . rawurlencode($this->account($service)) . $suffix, $body);
        } catch (Exception $e) {
            $this->Input->setErrors(['api' => ['internal' => $e->getMessage()]]);
        }
        return null;
    }

    public function suspendService($package, $service, $parent_package = null, $parent_service = null)
    {
        return $this->simple($service, 'POST', '/suspend', ['reason' => 'Suspended by billing']);
    }

    public function unsuspendService($package, $service, $parent_package = null, $parent_service = null)
    {
        return $this->simple($service, 'POST', '/unsuspend', new stdClass());
    }

    public function cancelService($package, $service, $parent_package = null, $parent_service = null)
    {
        return $this->simple($service, 'DELETE', '');
    }

    public function changeServicePackage($package_from, $package_to, $service, $parent_package = null, $parent_service = null)
    {
        return $this->simple($service, 'PUT', '/package', ['package' => $package_to->meta->package]);
    }

    public function editService($package, $service, array $vars = null, $parent_package = null, $parent_service = null)
    {
        $fields = $this->serviceFieldsToObject($service->fields);
        if (!empty($vars['ike_password']) && $vars['ike_password'] !== $fields->ike_password) {
            $this->simple($service, 'PUT', '/password', ['password' => $vars['ike_password']]);
            if ($this->Input->errors()) {
                return;
            }
            $fields->ike_password = $vars['ike_password'];
        }
        return [
            ['key' => 'ike_domain', 'value' => $fields->ike_domain, 'encrypted' => 0],
            ['key' => 'ike_account', 'value' => $fields->ike_account, 'encrypted' => 0],
            ['key' => 'ike_password', 'value' => $fields->ike_password, 'encrypted' => 1],
        ];
    }

    // ------------------------------------------------------------ client tab: one-click sign-in

    public function getClientTabs($package)
    {
        return ['tabClientLogin' => Language::_('Ike.tab_login', true)];
    }

    public function tabClientLogin($package, $service, array $get = null, array $post = null, array $files = null)
    {
        if (!empty($post['ike_sso'])) {
            try {
                $res = $this->call($this->getModuleRow($service->module_row_id), 'POST',
                    '/provision/accounts/' . rawurlencode($this->account($service)) . '/sso', new stdClass());
                header('Location: ' . $res['url']);
                exit;
            } catch (Exception $e) {
                $this->Input->setErrors(['api' => ['internal' => $e->getMessage()]]);
            }
        }
        return '<form method="post"><input type="hidden" name="ike_sso" value="1">'
            . '<input type="hidden" name="_csrf_token" value="' . htmlspecialchars($this->csrfToken(), ENT_QUOTES) . '">'
            . '<button class="btn btn-primary" type="submit">' . htmlspecialchars(Language::_('Ike.tab_login.button', true)) . '</button></form>';
    }

    private function csrfToken()
    {
        Loader::loadComponents($this, ['Session']);
        return (string) $this->Session->read('_csrf_token');
    }

    // ------------------------------------------------------------ HTTP

    private function call($row, $method, $path, $body = null)
    {
        $meta = isset($row->meta) ? $row->meta : $row;
        $url = 'https://' . $meta->hostname . ':' . ($meta->port ?: '2087') . '/api/v1' . $path;
        $ch = curl_init($url);
        $headers = ['Authorization: Bearer ' . $meta->token, 'Accept: application/json'];
        curl_setopt($ch, CURLOPT_CUSTOMREQUEST, $method);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
        curl_setopt($ch, CURLOPT_TIMEOUT, 300);
        // Certificates are checked. Only while the panel still has its self-signed
        // certificate, set IKE_ALLOW_SELF_SIGNED=1 in the web server's environment;
        // better, give the panel a real certificate (docs/INTEGRATIONS.md).
        curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, getenv('IKE_ALLOW_SELF_SIGNED') !== '1');
        curl_setopt($ch, CURLOPT_SSL_VERIFYHOST, getenv('IKE_ALLOW_SELF_SIGNED') === '1' ? 0 : 2);
        if ($body !== null) {
            $headers[] = 'Content-Type: application/json';
            curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($body));
        }
        curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);
        $raw = curl_exec($ch);
        $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $err = curl_error($ch);
        curl_close($ch);
        $logged = is_array($body) && isset($body['password']) ? array_merge($body, ['password' => '********']) : $body;
        $this->log($meta->hostname, json_encode(['method' => $method, 'path' => $path, 'body' => $logged]), 'input', true);
        $this->log($meta->hostname, (string) $raw, 'output', $code > 0 && $code < 300);
        if ($raw === false) {
            throw new Exception('Could not reach the Ike panel: ' . $err);
        }
        $data = json_decode($raw, true);
        if ($code >= 300) {
            throw new Exception(is_array($data) && isset($data['error']) ? $data['error'] : 'HTTP ' . $code);
        }
        return $data;
    }
}

<div class="text-center" style="padding:20px 0">
    <p>Manage websites, email, databases and files in the hosting panel.</p>
    <form method="post" action="clientarea.php?action=productdetails&amp;id={$serviceid}">
        <input type="hidden" name="dosinglesignon" value="1">
        <button type="submit" class="btn btn-primary">Log in to the control panel</button>
    </form>
    <p class="text-muted small" style="margin-top:12px">Or sign in at <a href="{$panelUrl}" target="_blank" rel="noopener">{$panelUrl}</a></p>
</div>

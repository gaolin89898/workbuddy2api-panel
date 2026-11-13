<script setup>
import WebAccountLogin from './WebAccountLogin.vue';
defineProps({ model: Object });
</script>

<template>
  <WebAccountLogin :model="model" provider="doubao" name="豆包" />
</template>

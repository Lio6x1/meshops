import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import './style.css'
import App from './App.vue'
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: () => import('./views/Entities.vue'), props: { overview: true } },
    {
      path: '/entities',
      component: () => import('./views/Entities.vue'),
      props: { overview: false },
    },
    { path: '/tasks', component: () => import('./views/Tasks.vue') },
    { path: '/tasks/:id', component: () => import('./views/TaskDetail.vue') },
    { path: '/search', component: () => import('./views/Search.vue') },
    { path: '/dispatch', component: () => import('./views/Dispatch.vue') },
    { path: '/status', component: () => import('./views/Status.vue') },
    { path: '/simulation', component: () => import('./views/Simulation.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
createApp(App).use(router).use(ElementPlus, { locale: zhCn }).mount('#app')
